/**
 * Cloudflare Access token verification.
 *
 * Access puts a signed JWT in the `Cf-Access-Jwt-Assertion` header on every
 * request it lets through. This file verifies that JWT. Nothing else in the
 * hub may treat an Access header as proof of identity.
 *
 * The `Cf-Access-Authenticated-User-Email` header is NOT used as an identity.
 * Any caller that reaches the Worker directly can set it. The email comes from
 * the verified claims instead.
 *
 * The verification is written against WebCrypto rather than a JWT library.
 * Access signs with RS256 only, so the work is one `crypto.subtle.verify` call
 * over `<header>.<payload>` plus four claim checks. A library would add a
 * dependency for that.
 *
 * WHAT IS CHECKED, in order. Any failure returns null and the caller answers
 * 401:
 *
 *   1. The token has three base64url parts.
 *   2. The header names alg RS256 and carries a `kid`.
 *   3. That `kid` is in the team's JWKS.
 *   4. The signature verifies against that key.
 *   5. `aud` contains ACCESS_AUD.
 *   6. `iss` is the team domain.
 *   7. `exp` is in the future and `nbf`/`iat` are not in the future, both
 *      within a 60-second clock skew allowance.
 */

/** How long a fetched JWKS stays usable before the hub fetches it again. */
const JWKS_TTL_MS = 60 * 60 * 1000;

/**
 * How far a clock may be out before a token is refused. Access signs on
 * Cloudflare's clock and the Worker checks on Cloudflare's clock, so this is
 * small on purpose.
 */
const CLOCK_SKEW_SECONDS = 60;

/** A JWKS fetch that hangs must not hold the request open. */
const JWKS_FETCH_TIMEOUT_MS = 5_000;

/** The RSA public keys of one Access team, keyed by `kid`. */
interface JwksCacheEntry {
  keys: Map<string, CryptoKey>;
  fetchedAtMs: number;
}

/**
 * The JWKS cache.
 *
 * THE CACHING CHOICE: in-memory, per isolate, with a one-hour TTL, keyed by
 * team domain. It is a plain module-level Map, so it lives as long as the
 * isolate does and dies with it.
 *
 * Why not KV: a KV round trip costs about as much as the JWKS fetch it would
 * replace, and it would put a shared, writable copy of the trust anchor in
 * storage. Why not a longer TTL: Access rotates its signing keys roughly every
 * six weeks and publishes the new key before it signs with it, so one hour is
 * far inside the window. A cold isolate pays one fetch; every later request in
 * that isolate pays none.
 *
 * An unknown `kid` also forces one refetch (see `getVerificationKey`), so a key
 * rotation is picked up without waiting out the TTL.
 */
const jwksCache = new Map<string, JwksCacheEntry>();

/** One key as the Access JWKS endpoint publishes it. */
interface JsonWebKey {
  kid?: string;
  kty?: string;
  alg?: string;
  use?: string;
  n?: string;
  e?: string;
}

/** The claims the hub reads. Access sets more; the rest are ignored. */
export interface AccessClaims {
  aud?: string | string[];
  email?: string;
  sub?: string;
  iss?: string;
  exp?: number;
  iat?: number;
  nbf?: number;
  identity_nonce?: string;
}

/** The result of a successful verification. */
export interface VerifiedAccessToken {
  claims: AccessClaims;
  /** The identity to act on: the verified email, or `sub` when there is none. */
  identity: string;
}

/** Decodes one base64url segment of a JWT to bytes. */
function decodeSegment(segment: string): Uint8Array {
  const padded = segment.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded.padEnd(Math.ceil(padded.length / 4) * 4, "="));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** Decodes one base64url segment of a JWT to a parsed JSON object. */
function decodeJsonSegment(segment: string): Record<string, unknown> | null {
  try {
    const text = new TextDecoder().decode(decodeSegment(segment));
    const value: unknown = JSON.parse(text);
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    return value as Record<string, unknown>;
  } catch {
    return null;
  }
}

/**
 * Normalises the team domain a deployer configured.
 *
 * A deployer may paste "myteam", "myteam.cloudflareaccess.com" or the full
 * "https://myteam.cloudflareaccess.com/". All three name the same team, and
 * getting this wrong would fail closed with an unhelpful 401.
 */
export function normalizeTeamDomain(configured: string): string | null {
  const trimmed = configured.trim().replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  if (!trimmed) return null;
  if (trimmed.includes("/")) return null;
  return trimmed.includes(".") ? trimmed.toLowerCase() : `${trimmed.toLowerCase()}.cloudflareaccess.com`;
}

/** The issuer Access puts in the token for a given team domain. */
function issuerForTeam(teamDomain: string): string {
  return `https://${teamDomain}`;
}

/** Imports one JWKS entry as an RS256 verification key. */
async function importJwk(jwk: JsonWebKey): Promise<CryptoKey | null> {
  if (jwk.kty !== "RSA" || !jwk.n || !jwk.e) return null;
  if (jwk.alg && jwk.alg !== "RS256") return null;
  try {
    return await crypto.subtle.importKey(
      "jwk",
      { kty: "RSA", n: jwk.n, e: jwk.e, alg: "RS256", ext: true },
      { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
      false,
      ["verify"],
    );
  } catch {
    return null;
  }
}

/** Fetches and imports the team's JWKS. Returns null when it cannot. */
async function fetchJwks(teamDomain: string): Promise<JwksCacheEntry | null> {
  const url = `${issuerForTeam(teamDomain)}/cdn-cgi/access/certs`;
  let response: Response;
  try {
    response = await fetch(url, {
      // The signal keeps a stalled JWKS host from holding the request open.
      signal: AbortSignal.timeout(JWKS_FETCH_TIMEOUT_MS),
      headers: { Accept: "application/json" },
    });
  } catch {
    return null;
  }
  if (!response.ok) return null;

  let document: unknown;
  try {
    document = await response.json();
  } catch {
    return null;
  }
  const keyList = (document as { keys?: JsonWebKey[] } | null)?.keys;
  if (!Array.isArray(keyList)) return null;

  const keys = new Map<string, CryptoKey>();
  for (const jwk of keyList) {
    if (!jwk?.kid) continue;
    const key = await importJwk(jwk);
    if (key) keys.set(jwk.kid, key);
  }
  if (keys.size === 0) return null;

  const entry: JwksCacheEntry = { keys, fetchedAtMs: Date.now() };
  jwksCache.set(teamDomain, entry);
  return entry;
}

/**
 * Returns the verification key for `kid`, fetching the JWKS when the cache is
 * empty, stale, or does not hold that `kid`.
 *
 * The unknown-`kid` refetch is what makes a key rotation take effect at once
 * instead of at the end of the TTL. It is also a way for a caller to make the
 * hub fetch: a token with a random `kid` forces one JWKS request. That costs a
 * subrequest against a Cloudflare-hosted endpoint and reveals nothing, and the
 * request still ends in a 401.
 */
async function getVerificationKey(teamDomain: string, kid: string): Promise<CryptoKey | null> {
  const cached = jwksCache.get(teamDomain);
  const fresh = cached && Date.now() - cached.fetchedAtMs < JWKS_TTL_MS;
  if (fresh) {
    const key = cached.keys.get(kid);
    if (key) return key;
  }
  const refreshed = await fetchJwks(teamDomain);
  return refreshed?.keys.get(kid) ?? null;
}

/** Empties the JWKS cache. Tests use it; nothing in the request path does. */
export function resetJwksCache(): void {
  jwksCache.clear();
}

/**
 * Verifies a Cloudflare Access JWT.
 *
 * Returns the verified claims, or null when the token is missing, malformed,
 * unsigned by the team, addressed to another application, or out of its
 * validity window. The reason is deliberately not returned: the caller answers
 * 401 either way, and a reason would tell a prober which check it failed.
 */
export async function verifyAccessToken(
  token: string,
  teamDomainConfigured: string,
  expectedAud: string,
): Promise<VerifiedAccessToken | null> {
  const teamDomain = normalizeTeamDomain(teamDomainConfigured);
  if (!teamDomain || !expectedAud.trim()) return null;

  const parts = token.split(".");
  if (parts.length !== 3) return null;
  const [headerSegment, payloadSegment, signatureSegment] = parts as [string, string, string];

  const header = decodeJsonSegment(headerSegment);
  if (!header) return null;
  // Only RS256 is accepted. Refusing "none" and the HS* family here is what
  // stops a caller signing a token with a key it chose itself.
  if (header.alg !== "RS256") return null;
  const kid = header.kid;
  if (typeof kid !== "string" || !kid) return null;

  const key = await getVerificationKey(teamDomain, kid);
  if (!key) return null;

  let signature: Uint8Array;
  try {
    signature = decodeSegment(signatureSegment);
  } catch {
    return null;
  }

  const signedBytes = new TextEncoder().encode(`${headerSegment}.${payloadSegment}`);
  let signatureOk: boolean;
  try {
    signatureOk = await crypto.subtle.verify(
      { name: "RSASSA-PKCS1-v1_5" },
      key,
      signature,
      signedBytes,
    );
  } catch {
    return null;
  }
  if (!signatureOk) return null;

  const claims = decodeJsonSegment(payloadSegment) as AccessClaims | null;
  if (!claims) return null;

  // `aud` is an array in Access tokens, but the JWT spec allows a bare string.
  const audiences = Array.isArray(claims.aud) ? claims.aud : claims.aud ? [claims.aud] : [];
  if (!audiences.includes(expectedAud.trim())) return null;

  if (claims.iss !== issuerForTeam(teamDomain)) return null;

  const nowSeconds = Math.floor(Date.now() / 1000);
  if (typeof claims.exp !== "number" || claims.exp + CLOCK_SKEW_SECONDS <= nowSeconds) return null;
  if (typeof claims.nbf === "number" && claims.nbf - CLOCK_SKEW_SECONDS > nowSeconds) return null;
  if (typeof claims.iat === "number" && claims.iat - CLOCK_SKEW_SECONDS > nowSeconds) return null;

  // The identity comes from the verified claims, never from a header. A
  // service token has no email, so `sub` stands in for it.
  const identity =
    typeof claims.email === "string" && claims.email
      ? claims.email
      : typeof claims.sub === "string" && claims.sub
        ? claims.sub
        : null;
  if (!identity) return null;

  return { claims, identity };
}
