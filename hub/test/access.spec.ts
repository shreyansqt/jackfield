/**
 * Tests for Cloudflare Access token verification.
 *
 * The case that matters most is the last one in the first block: a request
 * that carries `Cf-Access-Authenticated-User-Email` and no valid JWT must be
 * refused. That header is what the hub used to trust, and anybody who can
 * reach the Worker can set it.
 *
 * The tests mint their own RSA keypair, publish it as a JWKS through a stubbed
 * `globalThis.fetch`, and sign tokens with it. No network call leaves the test
 * and no Access account is needed. The stub also counts requests, which is how
 * the cache test observes the caching.
 */
import { env, createExecutionContext, waitOnExecutionContext } from "cloudflare:test";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import worker from "../src/index.js";
import { authenticateHuman } from "../src/auth.js";
import { resetJwksCache, verifyAccessToken } from "../src/access.js";
import type { Env } from "../src/env.js";

const ORIGIN = "https://hub.test";
const TEAM_DOMAIN = "jackfield.cloudflareaccess.com";
const AUD = "test-access-application-audience-tag";
const JWKS_URL = `https://${TEAM_DOMAIN}/cdn-cgi/access/certs`;

/** The env a deployed hub has once Access is on. */
function accessEnv(overrides: Partial<Env> = {}): Env {
  return {
    ...env,
    ACCESS_ENABLED: "true",
    ACCESS_TEAM_DOMAIN: TEAM_DOMAIN,
    ACCESS_AUD: AUD,
    DEV_SIGNIN_TOKEN: undefined,
    ...overrides,
  };
}

/** Base64url without padding, the encoding every JWT segment uses. */
function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function encodeJson(value: unknown): string {
  return toBase64Url(new TextEncoder().encode(JSON.stringify(value)));
}

const RSA_PARAMS = {
  name: "RSASSA-PKCS1-v1_5",
  modulusLength: 2048,
  publicExponent: new Uint8Array([1, 0, 1]),
  hash: "SHA-256",
} as const;

/** One signing identity: a keypair, its `kid`, and its JWKS entry. */
interface TestSigner {
  kid: string;
  privateKey: CryptoKey;
  jwk: Record<string, unknown>;
}

async function createSigner(kid: string): Promise<TestSigner> {
  const pair = (await crypto.subtle.generateKey(RSA_PARAMS, true, [
    "sign",
    "verify",
  ])) as CryptoKeyPair;
  const exported = (await crypto.subtle.exportKey("jwk", pair.publicKey)) as JsonWebKey;
  return {
    kid,
    privateKey: pair.privateKey,
    jwk: { kty: "RSA", alg: "RS256", use: "sig", kid, n: exported.n, e: exported.e },
  };
}

/** Signs a token with `signer`, over claims that default to a valid set. */
async function signToken(
  signer: TestSigner,
  claims: Record<string, unknown> = {},
  header: Record<string, unknown> = {},
): Promise<string> {
  const nowSeconds = Math.floor(Date.now() / 1000);
  const payload = {
    aud: [AUD],
    email: "shreyans@example.com",
    sub: "user-id-1",
    iss: `https://${TEAM_DOMAIN}`,
    iat: nowSeconds - 10,
    nbf: nowSeconds - 10,
    exp: nowSeconds + 600,
    ...claims,
  };
  const signingInput = `${encodeJson({ alg: "RS256", kid: signer.kid, typ: "JWT", ...header })}.${encodeJson(payload)}`;
  const signature = await crypto.subtle.sign(
    { name: "RSASSA-PKCS1-v1_5" },
    signer.privateKey,
    new TextEncoder().encode(signingInput),
  );
  return `${signingInput}.${toBase64Url(new Uint8Array(signature))}`;
}

/* --------------- the stubbed JWKS endpoint --------------- */

let realFetch: typeof globalThis.fetch;
let jwksRequestCount = 0;
let publishedKeys: Record<string, unknown>[] = [];

/** Serves the published JWKS at the team's certs URL, and nothing else. */
function installFetchStub(): void {
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (url === JWKS_URL) {
      jwksRequestCount++;
      return Response.json({ keys: publishedKeys });
    }
    return realFetch(input as RequestInfo, init);
  }) as typeof globalThis.fetch;
}

/** The hub's own signer. Generated once; RSA keygen is slow. */
let signer: TestSigner;
let otherSigner: TestSigner;

beforeEach(async () => {
  realFetch = globalThis.fetch;
  installFetchStub();
  signer ??= await createSigner("access-key-1");
  otherSigner ??= await createSigner("attacker-key");
  publishedKeys = [signer.jwk];
  jwksRequestCount = 0;
  resetJwksCache();
});

afterEach(() => {
  globalThis.fetch = realFetch;
  resetJwksCache();
});

/** Sends one request through the whole Worker with the given env. */
async function call(request: Request, workerEnv: Env): Promise<Response> {
  const ctx = createExecutionContext();
  const response = await worker.fetch(request, workerEnv, ctx);
  await waitOnExecutionContext(ctx);
  return response;
}

describe("Access verification accepts only a real token", () => {
  it("accepts a token this team signed, and takes the identity from the claims", async () => {
    const token = await signToken(signer);

    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: {
          "Cf-Access-Jwt-Assertion": token,
          // A lie. The claims win, so the caller is shreyans@example.com.
          "Cf-Access-Authenticated-User-Email": "attacker@example.com",
        },
      }),
      accessEnv(),
    );

    expect(caller).not.toBeNull();
    expect(caller!.identity).toBe("shreyans@example.com");
    expect(caller!.viaAccess).toBe(true);
  });

  it("refuses a token signed by a key the team does not publish", async () => {
    // The attacker signs a well-formed token with their own key, and even
    // names the team's kid. The JWKS does not hold their key, and the
    // signature does not verify against the one it does hold.
    const forged = await signToken({ ...otherSigner, kid: signer.kid });

    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, { headers: { "Cf-Access-Jwt-Assertion": forged } }),
      accessEnv(),
    );

    expect(caller).toBeNull();
  });

  it("refuses a token whose payload was edited after signing", async () => {
    const token = await signToken(signer);
    const [header, , signature] = token.split(".") as [string, string, string];
    const tampered = `${header}.${encodeJson({
      aud: [AUD],
      email: "attacker@example.com",
      iss: `https://${TEAM_DOMAIN}`,
      exp: Math.floor(Date.now() / 1000) + 600,
    })}.${signature}`;

    expect(await verifyAccessToken(tampered, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses a token addressed to another Access application", async () => {
    const token = await signToken(signer, { aud: ["a-different-application-tag"] });
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses an expired token", async () => {
    const nowSeconds = Math.floor(Date.now() / 1000);
    const token = await signToken(signer, {
      iat: nowSeconds - 7200,
      nbf: nowSeconds - 7200,
      exp: nowSeconds - 3600,
    });
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses a token that is not valid yet", async () => {
    const nowSeconds = Math.floor(Date.now() / 1000);
    const token = await signToken(signer, { nbf: nowSeconds + 3600, exp: nowSeconds + 7200 });
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses a token issued by another team", async () => {
    const token = await signToken(signer, { iss: "https://someoneelse.cloudflareaccess.com" });
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses an unsigned token, whatever its header claims", async () => {
    const nowSeconds = Math.floor(Date.now() / 1000);
    const payload = encodeJson({
      aud: [AUD],
      email: "attacker@example.com",
      iss: `https://${TEAM_DOMAIN}`,
      exp: nowSeconds + 600,
    });

    const algNone = `${encodeJson({ alg: "none", kid: signer.kid })}.${payload}.`;
    expect(await verifyAccessToken(algNone, TEAM_DOMAIN, AUD)).toBeNull();

    // RS256 in the header but no real signature behind it.
    const emptySignature = `${encodeJson({ alg: "RS256", kid: signer.kid })}.${payload}.`;
    expect(await verifyAccessToken(emptySignature, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses malformed input rather than throwing", async () => {
    for (const bad of ["", "not-a-jwt", "a.b", "a.b.c", "....", "%%%.%%%.%%%"]) {
      expect(await verifyAccessToken(bad, TEAM_DOMAIN, AUD)).toBeNull();
    }
  });

  it("refuses every token when the team domain or aud is unconfigured", async () => {
    const token = await signToken(signer);
    expect(await verifyAccessToken(token, "", AUD)).toBeNull();
    expect(await verifyAccessToken(token, TEAM_DOMAIN, "")).toBeNull();
  });

  it("accepts a team domain written without the cloudflareaccess.com suffix", async () => {
    const token = await signToken(signer);
    const verified = await verifyAccessToken(token, "jackfield", AUD);
    expect(verified?.identity).toBe("shreyans@example.com");
  });

  it("reads the token from the CF_Authorization cookie when the header is absent", async () => {
    const token = await signToken(signer);
    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: { Cookie: `other=x; CF_Authorization=${token}` },
      }),
      accessEnv(),
    );
    expect(caller?.identity).toBe("shreyans@example.com");
  });

  it("falls back to sub when the token carries no email", async () => {
    const token = await signToken(signer, { email: undefined, sub: "service-token-id" });
    const verified = await verifyAccessToken(token, TEAM_DOMAIN, AUD);
    expect(verified?.identity).toBe("service-token-id");
  });
});

describe("the forged header, which is what this work closes", () => {
  it("refuses a caller who sends only Cf-Access-Authenticated-User-Email", async () => {
    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: { "Cf-Access-Authenticated-User-Email": "attacker@example.com" },
      }),
      accessEnv(),
    );
    expect(caller).toBeNull();
  });

  it("refuses that caller at the approvals endpoint, so no ticket is minted", async () => {
    const response = await call(
      new Request(`${ORIGIN}/approvals`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Cf-Access-Authenticated-User-Email": "attacker@example.com",
          "Cf-Access-Jwt-Assertion": "not.a.real-token",
        },
        body: JSON.stringify({ connection: "slack-forged" }),
      }),
      accessEnv(),
    );

    expect(response.status).toBe(401);
    expect(await response.text()).not.toContain("approval_ticket");
  });

  it("mints a ticket for a caller who holds a verified token", async () => {
    const token = await signToken(signer);
    const response = await call(
      new Request(`${ORIGIN}/approvals`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Cf-Access-Jwt-Assertion": token,
        },
        body: JSON.stringify({ connection: "slack-verified" }),
      }),
      accessEnv(),
    );

    expect(response.status).toBe(200);
    const body = (await response.json()) as { approval_ticket: string };
    expect(typeof body.approval_ticket).toBe("string");
  });

  it("ignores the development sign-in token once Access is on", async () => {
    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: { Authorization: "Bearer test-dev-signin-token" },
      }),
      // Even with the secret still set, the Access branch never reads it.
      accessEnv({ DEV_SIGNIN_TOKEN: "test-dev-signin-token" }),
    );
    expect(caller).toBeNull();
  });
});

describe("the JWKS cache", () => {
  it("fetches the keys once per isolate, not once per request", async () => {
    const token = await signToken(signer);

    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).not.toBeNull();
    expect(jwksRequestCount).toBe(1);

    for (let i = 0; i < 5; i++) {
      expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).not.toBeNull();
    }
    expect(jwksRequestCount).toBe(1);
  });

  it("refetches when it meets a kid it does not hold, so a rotation lands at once", async () => {
    const token = await signToken(signer);
    await verifyAccessToken(token, TEAM_DOMAIN, AUD);
    expect(jwksRequestCount).toBe(1);

    // The team rotates: a new key signs, and the JWKS now publishes both.
    const rotated = await createSigner("access-key-2");
    publishedKeys = [signer.jwk, rotated.jwk];
    const rotatedToken = await signToken(rotated);

    const verified = await verifyAccessToken(rotatedToken, TEAM_DOMAIN, AUD);
    expect(verified?.identity).toBe("shreyans@example.com");
    expect(jwksRequestCount).toBe(2);
  });

  it("refuses the caller when the JWKS endpoint cannot be reached", async () => {
    globalThis.fetch = (async () => {
      throw new Error("network down");
    }) as typeof globalThis.fetch;

    const token = await signToken(signer);
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });

  it("refuses the caller when the JWKS endpoint answers with an error", async () => {
    globalThis.fetch = (async () =>
      new Response("nope", { status: 500 })) as typeof globalThis.fetch;

    const token = await signToken(signer);
    expect(await verifyAccessToken(token, TEAM_DOMAIN, AUD)).toBeNull();
  });
});

describe("the development sign-in still works when Access is off", () => {
  it("accepts the development token", async () => {
    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: { Authorization: "Bearer test-dev-signin-token" },
      }),
      env,
    );
    expect(caller?.identity).toBe("development-signin");
    expect(caller?.viaAccess).toBe(false);
  });

  it("ignores an Access header when Access is off", async () => {
    const caller = await authenticateHuman(
      new Request(`${ORIGIN}/approvals`, {
        headers: { "Cf-Access-Authenticated-User-Email": "attacker@example.com" },
      }),
      env,
    );
    expect(caller).toBeNull();
  });
});
