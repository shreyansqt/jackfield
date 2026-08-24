/**
 * The door: how a caller proves who it is.
 *
 * The hub recognises two kinds of caller, and they have different powers.
 *
 * - A DEVICE holds a bearer token from `jf login`. A device may READ a
 *   credential. A device may never WRITE one.
 * - A HUMAN is a person in a browser, identified by Cloudflare Access. Only a
 *   human can approve a credential write, and the approval covers one write.
 *
 * This split is the point of the design, so it is enforced here in one place
 * and the route handlers call into it. See DESIGN.md.
 */
import type { Env } from "./env.js";
import { verifyAccessToken } from "./access.js";
import { timingSafeEqualStrings } from "./crypto.js";
import { lookupDeviceByToken, touchDevice, type DeviceRecord } from "./storage.js";

/** A caller that authenticated with a device token. */
export interface DeviceCaller {
  kind: "device";
  device: DeviceRecord;
  /** The raw token, so the caller can record its use. */
  token: string;
}

/** A caller that is a person in a browser. */
export interface HumanCaller {
  kind: "human";
  /** The identity Access asserted, normally an email address. */
  identity: string;
  /** True when a real Access assertion was verified, false in development. */
  viaAccess: boolean;
}

export type Caller = DeviceCaller | HumanCaller;

/**
 * Reads the Access token out of the `CF_Authorization` cookie.
 *
 * Access sets both the header and this cookie. The header is the normal path.
 * The cookie is the fallback for a request that reaches the Worker without it,
 * which happens on a browser navigation the edge did not re-decorate. The
 * cookie value is a JWT and is verified exactly like the header value, so
 * accepting it adds no trust.
 */
function readAccessCookie(request: Request): string | null {
  const cookies = request.headers.get("Cookie");
  if (!cookies) return null;
  for (const part of cookies.split(";")) {
    const separator = part.indexOf("=");
    if (separator < 0) continue;
    if (part.slice(0, separator).trim() !== "CF_Authorization") continue;
    const value = part.slice(separator + 1).trim();
    return value || null;
  }
  return null;
}

/** Pulls the bearer token out of an Authorization header. */
export function bearerToken(request: Request): string | null {
  const header = request.headers.get("Authorization");
  if (!header) return null;
  const match = /^Bearer\s+(.+)$/i.exec(header.trim());
  return match ? match[1]!.trim() : null;
}

/**
 * Authenticates a device token.
 *
 * Returns null when there is no token or the token is unknown. The caller
 * turns that into a 401. A revoked token is unknown, because revocation
 * deletes the record.
 *
 * The last-use timestamp is written through `ctx.waitUntil`, so recording the
 * use never delays the response.
 */
export async function authenticateDevice(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
): Promise<DeviceCaller | null> {
  const token = bearerToken(request);
  if (!token) return null;
  const device = await lookupDeviceByToken(env.HUB_KV, token);
  if (!device) return null;
  ctx.waitUntil(touchDevice(env.HUB_KV, token));
  return { kind: "device", device, token };
}

/**
 * Authenticates a person in a browser.
 *
 * When ACCESS_ENABLED is "true", the caller must present a
 * `Cf-Access-Jwt-Assertion` that verifies against the Access team's published
 * signing keys, carries ACCESS_AUD in its `aud`, and is inside its validity
 * window. `src/access.ts` performs those checks.
 *
 * The identity is the `email` claim of the verified token. The
 * `Cf-Access-Authenticated-User-Email` header is ignored, because any caller
 * that reaches the Worker directly can set it. Verification therefore holds
 * even on a Worker still reachable on `workers.dev`.
 *
 * Access in front of the Worker is still the right configuration — it stops
 * unauthenticated traffic before it costs anything — but the hub no longer
 * depends on it for correctness.
 *
 * When ACCESS_ENABLED is not "true", the hub runs its development sign-in:
 * the caller must present DEV_SIGNIN_TOKEN. That is a single shared secret and
 * it is not a substitute for Access. It exists so the flows can be exercised
 * and tested before Access is configured.
 */
export async function authenticateHuman(
  request: Request,
  env: Env,
): Promise<HumanCaller | null> {
  if (env.ACCESS_ENABLED === "true") {
    const assertion =
      request.headers.get("Cf-Access-Jwt-Assertion") ?? readAccessCookie(request);
    if (!assertion) return null;
    const verified = await verifyAccessToken(
      assertion,
      env.ACCESS_TEAM_DOMAIN ?? "",
      env.ACCESS_AUD ?? "",
    );
    if (!verified) return null;
    return { kind: "human", identity: verified.identity, viaAccess: true };
  }

  const expected = env.DEV_SIGNIN_TOKEN;
  if (!expected) return null;
  const presented = bearerToken(request) ?? new URL(request.url).searchParams.get("dev_token");
  if (!presented) return null;
  if (!(await timingSafeEqualStrings(presented, expected))) return null;
  return { kind: "human", identity: "development-signin", viaAccess: false };
}
