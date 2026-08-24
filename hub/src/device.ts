/**
 * The device-authorization flow, in the shape of RFC 8628.
 *
 * Why this is hand-written: `@cloudflare/workers-oauth-provider` implements no
 * device grant at all. Version 0.10.3 has no reference to `device_code` or
 * `urn:ietf:params:oauth:grant-type:device_code` anywhere in its code. The
 * brief said not to fight the library, so the device flow lives here instead
 * and the library keeps the browser OAuth front it does implement well.
 * DESIGN.md records this.
 *
 * The flow has three actors and four steps.
 *
 *   1. A headless machine calls POST /device/code. It receives a device code,
 *      a short user code, and a URL.
 *   2. The person reads the short code off that machine and types it into a
 *      browser on another device, at GET /device.
 *   3. The person approves. The hub mints a device token.
 *   4. The machine, which has been polling POST /device/token, collects the
 *      token on its next poll. The request record is then deleted.
 */
import type { Env } from "./env.js";
import { authenticateHuman, presentedDevToken } from "./auth.js";
import { devTokenField, escapeHtml, html, json, oauthError, page } from "./http.js";
import {
  approveDeviceAuth,
  consumeDeviceAuth,
  DEVICE_CODE_TTL_SECONDS,
  DEVICE_POLL_INTERVAL_SECONDS,
  getDeviceAuth,
  getDeviceAuthByUserCode,
  startDeviceAuth,
} from "./storage.js";

/**
 * POST /device/code — the machine starts the flow.
 *
 * The body may name the device, for example `device_name=grumpyorange`.
 * The name is what `jf devices` later shows, so the hub asks for it up front
 * rather than inventing one.
 */
export async function handleDeviceCode(request: Request, env: Env): Promise<Response> {
  const form = await readForm(request);
  const requestedName = (form.get("device_name") ?? "").trim() || "unnamed device";

  const record = await startDeviceAuth(env.HUB_KV, requestedName);
  const verificationUri = `${env.HUB_ORIGIN}/device`;

  return json({
    device_code: record.deviceCode,
    user_code: record.userCode,
    verification_uri: verificationUri,
    // The convenience form, with the code already filled in.
    verification_uri_complete: `${verificationUri}?user_code=${encodeURIComponent(record.userCode)}`,
    expires_in: DEVICE_CODE_TTL_SECONDS,
    interval: DEVICE_POLL_INTERVAL_SECONDS,
  });
}

/**
 * POST /device/token — the machine polls for its token.
 *
 * The responses follow RFC 8628 section 3.5:
 *   authorization_pending  the person has not approved yet, keep polling
 *   expired_token          the request timed out, start again
 *   access_denied          not used yet; reserved for an explicit refusal
 */
export async function handleDeviceToken(request: Request, env: Env): Promise<Response> {
  const form = await readForm(request);

  const grantType = form.get("grant_type");
  if (grantType !== "urn:ietf:params:oauth:grant-type:device_code") {
    return oauthError("unsupported_grant_type", "This endpoint serves the device grant only");
  }

  const deviceCode = form.get("device_code");
  if (!deviceCode) {
    return oauthError("invalid_request", "device_code is required");
  }

  const record = await getDeviceAuth(env.HUB_KV, deviceCode);
  if (!record) {
    // An unknown code and an expired code are the same thing to the client:
    // KV has already removed the expired record.
    return oauthError("expired_token", "This device code expired or is unknown");
  }

  if (record.expiresAt < Date.now()) {
    await consumeDeviceAuth(env.HUB_KV, record);
    return oauthError("expired_token", "This device code expired");
  }

  if (!record.approved || !record.issuedToken) {
    return oauthError("authorization_pending", "The user has not approved this device yet");
  }

  // The token is collected exactly once. Deleting the record here is what
  // stops a replayed poll from handing out a second token.
  const token = record.issuedToken;
  await consumeDeviceAuth(env.HUB_KV, record);

  return json({
    access_token: token,
    token_type: "Bearer",
    device_name: record.requestedName,
  });
}

/**
 * GET /device — the page where the person types the short code.
 * GET /device?user_code=BCDF-GHJK jumps straight to the confirmation.
 */
export async function handleDevicePage(request: Request, env: Env): Promise<Response> {
  const human = await authenticateHuman(request, env);
  if (!human) return signInRequired(env);

  const carry = devTokenField(presentedDevToken(request, env));

  const userCode = new URL(request.url).searchParams.get("user_code");
  if (!userCode) {
    return html(
      page(
        "Approve a device",
        `<h1>Approve a device</h1>
<p>Type the code shown on the machine you are signing in.</p>
<form method="post" action="/device/approve">${carry}
  <label for="user_code">Device code</label>
  <input type="text" id="user_code" name="user_code" autocomplete="off"
         autocapitalize="characters" placeholder="BCDF-GHJK" required>
  <p><button type="submit">Continue</button></p>
</form>`,
      ),
    );
  }

  const record = await getDeviceAuthByUserCode(env.HUB_KV, userCode);
  if (!record || record.expiresAt < Date.now()) {
    return html(
      page(
        "Code not found",
        `<h1>Code not found</h1>
<p>That code is unknown or it expired. Start <code>jf login</code> again on the
machine to get a new code.</p>`,
      ),
      404,
    );
  }

  return html(
    page(
      "Approve a device",
      `<h1>Approve this device?</h1>
<p>A machine asked for a jackfield device token.</p>
<p>Code: <span class="code">${escapeHtml(record.userCode)}</span></p>
<p>It named itself <strong>${escapeHtml(record.requestedName)}</strong>. You can
change the name before you approve. The name is what
<code>jf devices</code> shows.</p>
<form method="post" action="/device/approve">${carry}
  <input type="hidden" name="user_code" value="${escapeHtml(record.userCode)}">
  <label for="device_name">Device name</label>
  <input type="text" id="device_name" name="device_name"
         value="${escapeHtml(record.requestedName)}" required>
  <p><button type="submit">Approve this device</button></p>
</form>
<p class="warn">Approve this only if you started the login yourself. A device
token can read every credential in this hub.</p>`,
    ),
  );
}

/**
 * POST /device/approve — the person approves, and the hub mints the token.
 *
 * The form is read BEFORE the identity check, because a request body can be
 * read only once and the development sign-in token may arrive inside it. The
 * page puts it there, since a form submission does not carry the query string
 * the browser was opened with.
 */
export async function handleDeviceApprove(request: Request, env: Env): Promise<Response> {
  const form = await readForm(request);

  const human = await authenticateHuman(request, env, form.get("dev_token"));
  if (!human) return signInRequired(env);

  const userCode = (form.get("user_code") ?? "").trim();
  if (!userCode) {
    return html(page("Missing code", `<h1>Missing code</h1><p>No device code was given.</p>`), 400);
  }

  const record = await getDeviceAuthByUserCode(env.HUB_KV, userCode);
  if (!record || record.expiresAt < Date.now()) {
    return html(
      page(
        "Code not found",
        `<h1>Code not found</h1>
<p>That code is unknown or it expired. Start <code>jf login</code> again.</p>`,
      ),
      404,
    );
  }

  if (record.approved) {
    return html(
      page(
        "Already approved",
        `<h1>Already approved</h1>
<p>This device was approved. Nothing more to do here.</p>`,
      ),
    );
  }

  const name = (form.get("device_name") ?? "").trim() || record.requestedName;
  const device = await approveDeviceAuth(env.HUB_KV, record, name);

  return html(
    page(
      "Device approved",
      `<h1>Device approved</h1>
<p><strong>${escapeHtml(device.name)}</strong> now has a device token.</p>
<p>Return to the machine. It collects the token on its next poll, within
${DEVICE_POLL_INTERVAL_SECONDS} seconds.</p>
<p>To take this access away later, run
<code>jf devices revoke ${escapeHtml(device.name)}</code>.</p>`,
    ),
  );
}

/** The page shown when a browser caller has not signed in. */
function signInRequired(env: Env): Response {
  const viaAccess = env.ACCESS_ENABLED === "true";
  return html(
    page(
      "Sign in required",
      viaAccess
        ? `<h1>Sign in required</h1>
<p>Cloudflare Access did not identify you. Open this page again through your
Access sign-in.</p>`
        : `<h1>Sign in required</h1>
<p>Cloudflare Access is not configured on this hub yet.</p>
<p>Until it is, browser pages need the development sign-in token. Add
<code>?dev_token=...</code> to the URL, or send it as a bearer token.</p>
<p class="warn">The development sign-in is one shared secret. Configure Access
before you store a real credential in this hub.</p>`,
    ),
    401,
  );
}

/**
 * Reads a request body as form data.
 * The device endpoints accept `application/x-www-form-urlencoded`, which is
 * what RFC 8628 specifies, and JSON as a convenience for the `jf` client.
 */
async function readForm(request: Request): Promise<Map<string, string>> {
  const result = new Map<string, string>();
  const contentType = request.headers.get("Content-Type") ?? "";

  if (contentType.includes("application/json")) {
    const body = (await request.json()) as Record<string, unknown>;
    for (const [key, value] of Object.entries(body)) {
      if (typeof value === "string") result.set(key, value);
    }
    return result;
  }

  const form = await request.formData();
  for (const [key, value] of form.entries()) {
    if (typeof value === "string") result.set(key, value);
  }
  return result;
}
