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
 * PHASE 1 SEAM — Cloudflare Access is not integrated yet.
 *
 * When ACCESS_ENABLED is "true", the hub reads the identity from the
 * `Cf-Access-Authenticated-User-Email` header that Access sets, and requires
 * the `Cf-Access-Jwt-Assertion` header to be present. Access must be
 * configured to protect these paths in the Cloudflare dashboard, because a
 * header alone proves nothing if the origin is reachable without Access.
 *
 * THE REMAINING WORK, stated plainly: this function does not yet verify the
 * Access JWT signature against the team's public keys at
 * `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, nor check its
 * `aud` claim against ACCESS_AUD. Until that verification lands, the security
 * of the browser path rests entirely on Access being in front of the Worker
 * and the Worker not being reachable by any other route. Do not treat the
 * header as proof on a Worker that is also exposed on `workers.dev`.
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
    const assertion = request.headers.get("Cf-Access-Jwt-Assertion");
    const email = request.headers.get("Cf-Access-Authenticated-User-Email");
    if (!assertion || !email) return null;
    return { kind: "human", identity: email, viaAccess: true };
  }

  const expected = env.DEV_SIGNIN_TOKEN;
  if (!expected) return null;
  const presented = bearerToken(request) ?? new URL(request.url).searchParams.get("dev_token");
  if (!presented) return null;
  if (!(await timingSafeEqualStrings(presented, expected))) return null;
  return { kind: "human", identity: "development-signin", viaAccess: false };
}
