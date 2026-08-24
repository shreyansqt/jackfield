/**
 * The credential API.
 *
 * The rule this file exists to enforce:
 *
 *   READ  a credential -> a device token is enough.
 *   WRITE a credential -> a fresh browser approval is required, every time.
 *                          A device token is never enough, and holding one
 *                          gives no help at all in getting a write through.
 *
 * A device reads credentials constantly, so the read path is cheap. A human
 * writes a credential rarely and is present when it happens, so the write path
 * can afford to be expensive.
 */
import type { Env } from "./env.js";
import { authenticateDevice, authenticateHuman } from "./auth.js";
import { escapeHtml, html, json, page } from "./http.js";
import {
  consumeApproval,
  createApproval,
  getCredentialRecord,
  getCredentialSecret,
  listCredentials,
  listDevices,
  putCredential,
  revokeDevice,
} from "./storage.js";

/**
 * GET /creds/:connection — read one credential. A device token is required.
 *
 * The response carries the decrypted secret. That is the entire purpose of the
 * endpoint: the `jf` shim calls it at launch and passes the value to the CLI
 * it wraps. The response is marked no-store so no cache on the path keeps it.
 */
export async function handleGetCredential(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
  connection: string,
): Promise<Response> {
  const caller = await authenticateDevice(request, env, ctx);
  if (!caller) {
    return json(
      { error: "unauthorized", error_description: "A device token is required. Run `jf login`." },
      401,
      { "WWW-Authenticate": 'Bearer realm="jackfield"' },
    );
  }

  const found = await getCredentialSecret(env.HUB_KV, env.CRED_MASTER_KEY, connection);
  if (!found) {
    return json(
      {
        error: "not_found",
        error_description: `No credential is stored for "${connection}". Run \`jf auth ${connection}\`.`,
      },
      404,
    );
  }

  return json(
    {
      connection: found.record.connection,
      secret: found.secret,
      identity: found.record.identity,
      updated_at: found.record.updatedAt,
    },
    200,
    { "Cache-Control": "no-store" },
  );
}

/**
 * PUT /creds/:connection — write one credential.
 *
 * This requires an approval ticket from `POST /approvals`, which only a
 * browser human can obtain. The ticket is spent here: it covers this one
 * connection, and `consumeApproval` deletes it whether or not it matched.
 *
 * A device token on this request is ignored. It is not an alternative and it
 * is not an addition. The check below never inspects the Authorization header
 * for a device token, on purpose.
 */
export async function handlePutCredential(
  request: Request,
  env: Env,
  connection: string,
): Promise<Response> {
  const body = (await request.json().catch(() => null)) as {
    secret?: unknown;
    identity?: unknown;
    approval_ticket?: unknown;
  } | null;

  if (!body || typeof body.secret !== "string" || body.secret.length === 0) {
    return json({ error: "invalid_request", error_description: "secret is required" }, 400);
  }

  if (typeof body.approval_ticket !== "string" || body.approval_ticket.length === 0) {
    return json(
      {
        error: "approval_required",
        error_description:
          "Writing a credential needs a fresh browser approval. Run `jf auth` and complete the approval in your browser.",
      },
      403,
    );
  }

  const approval = await consumeApproval(env.HUB_KV, body.approval_ticket, connection);
  if (!approval) {
    return json(
      {
        error: "approval_invalid",
        error_description:
          "That approval is unknown, expired, already used, or was issued for another connection. Approve the write again.",
      },
      403,
    );
  }

  const identity = typeof body.identity === "string" ? body.identity : "unknown";
  const record = await putCredential(env.HUB_KV, env.CRED_MASTER_KEY, {
    connection,
    secret: body.secret,
    identity,
    updatedBy: approval.approvedBy,
  });

  return json({
    connection: record.connection,
    identity: record.identity,
    updated_at: record.updatedAt,
    updated_by: record.updatedBy,
  });
}

/**
 * GET /approvals?connection=<name> — the page where a person approves a write.
 *
 * `jf auth <connection>` opens this in a browser. The person sees which
 * connection is about to be written, and approves or closes the tab.
 *
 * The secret never passes through the browser. The browser only produces the
 * ticket; `jf` then sends the secret straight to the hub.
 */
export async function handleApprovalPage(request: Request, env: Env): Promise<Response> {
  const human = await authenticateHuman(request, env);
  if (!human) return approvalSignInRequired(env);

  const connection = new URL(request.url).searchParams.get("connection");
  if (!connection) {
    return html(
      page(
        "Approve a credential write",
        `<h1>Approve a credential write</h1>
<p>Name the connection you want to write.</p>
<form method="get" action="/approvals">
  <label for="connection">Connection</label>
  <input type="text" id="connection" name="connection" autocomplete="off"
         placeholder="slack-work" required>
  <p><button type="submit">Continue</button></p>
</form>`,
      ),
    );
  }

  return html(
    page(
      "Approve a credential write",
      `<h1>Approve this write?</h1>
<p>This stores a new credential for
<strong>${escapeHtml(connection)}</strong>, as
<strong>${escapeHtml(human.identity)}</strong>.</p>
<p>The approval lasts five minutes and covers this one connection. It permits
one write, and nothing else.</p>
<form method="post" action="/approvals">
  <input type="hidden" name="connection" value="${escapeHtml(connection)}">
  <p><button type="submit">Approve this write</button></p>
</form>
<p class="warn">Approve this only if you started <code>jf auth</code> yourself.</p>`,
    ),
  );
}

/**
 * POST /approvals — a human in a browser approves one credential write.
 *
 * The ticket is short-lived and covers exactly one connection.
 *
 * This answers in two shapes, because it has two callers. A form submission
 * from the approval page gets an HTML page that shows the ticket as text a
 * person can copy. Everything else gets the JSON that `jf` and curl parse.
 * The JSON shape is the original contract and it did not change.
 */
export async function handleCreateApproval(request: Request, env: Env): Promise<Response> {
  const wantsHtml = prefersHtml(request);

  const human = await authenticateHuman(request, env);
  if (!human) {
    if (wantsHtml) return approvalSignInRequired(env);
    return json(
      {
        error: "unauthorized",
        error_description:
          "Only a person signed in through the browser can approve a credential write.",
      },
      401,
    );
  }

  const connection = await readConnection(request);
  if (!connection) {
    if (wantsHtml) {
      return html(
        page(
          "Missing connection",
          `<h1>Missing connection</h1><p>No connection was named.</p>`,
        ),
        400,
      );
    }
    return json({ error: "invalid_request", error_description: "connection is required" }, 400);
  }

  const ticket = await createApproval(env.HUB_KV, connection, human.identity);

  if (wantsHtml) {
    return html(
      page(
        "Write approved",
        `<h1>Write approved</h1>
<p>Copy this ticket back into <code>jf auth</code>:</p>
<p><span class="code">${escapeHtml(ticket)}</span></p>
<p>It works once, for <strong>${escapeHtml(connection)}</strong> only, for five
minutes.</p>`,
      ),
      200,
      { "Cache-Control": "no-store" },
    );
  }

  return json({ approval_ticket: ticket, connection }, 200, {
    "Cache-Control": "no-store",
  });
}

/**
 * True when the caller is a browser rather than a script.
 *
 * A form submission sends `Accept: text/html` and a form content type. `jf`
 * and curl send neither, so they keep receiving JSON.
 */
function prefersHtml(request: Request): boolean {
  const accept = request.headers.get("Accept") ?? "";
  if (accept.includes("text/html")) return true;
  const contentType = request.headers.get("Content-Type") ?? "";
  return contentType.includes("application/x-www-form-urlencoded");
}

/** Reads the connection name from a JSON body, a form body, or the query. */
async function readConnection(request: Request): Promise<string | null> {
  const contentType = request.headers.get("Content-Type") ?? "";

  if (contentType.includes("application/x-www-form-urlencoded")) {
    const form = await request.formData();
    const value = form.get("connection");
    return typeof value === "string" && value.length > 0 ? value : null;
  }

  const body = (await request.json().catch(() => null)) as { connection?: unknown } | null;
  if (body && typeof body.connection === "string" && body.connection.length > 0) {
    return body.connection;
  }

  const fromQuery = new URL(request.url).searchParams.get("connection");
  return fromQuery && fromQuery.length > 0 ? fromQuery : null;
}

/** The page shown when the approval pages do not know who the caller is. */
function approvalSignInRequired(env: Env): Response {
  return html(
    page(
      "Sign in required",
      env.ACCESS_ENABLED === "true"
        ? `<h1>Sign in required</h1>
<p>Cloudflare Access did not identify you. Open this page again through your
Access sign-in.</p>`
        : `<h1>Sign in required</h1>
<p>Cloudflare Access is not configured on this hub yet.</p>
<p>Until it is, add <code>?dev_token=...</code> to the URL.</p>
<p class="warn">Configure Access before you store a real credential.</p>`,
    ),
    401,
  );
}

/**
 * GET /status — the liveness summary behind `jf status`.
 *
 * PHASE 1 SCOPE, stated honestly: this reports which credentials exist, who
 * they act as, and how old they are. It does NOT probe the upstream services,
 * so it cannot yet answer "is this token still accepted by Slack". A stored
 * credential that the upstream revoked still shows here as present. The real
 * probes are later work; the issue's "which of my connections is logged out
 * this morning" question is only partly answered until then.
 */
export async function handleStatus(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
): Promise<Response> {
  const caller = await authenticateDevice(request, env, ctx);
  if (!caller) {
    return json(
      { error: "unauthorized", error_description: "A device token is required. Run `jf login`." },
      401,
      { "WWW-Authenticate": 'Bearer realm="jackfield"' },
    );
  }

  const now = Date.now();
  const credentials = await listCredentials(env.HUB_KV);

  return json({
    connections: credentials
      .map((record) => ({
        connection: record.connection,
        identity: record.identity,
        updated_at: record.updatedAt,
        age_seconds: Math.floor((now - record.updatedAt) / 1000),
        // Always null in Phase 1. The field exists so the `jf` client can be
        // written against the final shape before the probes are built.
        upstream_ok: null,
      }))
      .sort((a, b) => a.connection.localeCompare(b.connection)),
    probes_implemented: false,
  });
}

/** GET /devices — list every issued device token, for `jf devices`. */
export async function handleListDevices(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
): Promise<Response> {
  const caller = await authenticateDevice(request, env, ctx);
  if (!caller) {
    return json({ error: "unauthorized", error_description: "A device token is required." }, 401);
  }

  const devices = await listDevices(env.HUB_KV);
  return json({
    devices: devices
      .map((device) => ({
        device_id: device.deviceId,
        name: device.name,
        created_at: device.createdAt,
        last_used_at: device.lastUsedAt,
        /** True for the device making this request, so `jf` can mark it. */
        current: device.deviceId === caller.device.deviceId,
      }))
      .sort((a, b) => a.created_at - b.created_at),
  });
}

/**
 * DELETE /devices/:deviceId — revoke one device token.
 *
 * A device may revoke another device, including itself. That is deliberate: a
 * lost laptop is revoked from the machine still in hand, and that machine has
 * only its own device token. Revocation removes access; it does not grant any,
 * so it does not need the stronger browser approval that a write needs.
 */
export async function handleRevokeDevice(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
  deviceId: string,
): Promise<Response> {
  const caller = await authenticateDevice(request, env, ctx);
  if (!caller) {
    return json({ error: "unauthorized", error_description: "A device token is required." }, 401);
  }

  const removed = await revokeDevice(env.HUB_KV, deviceId);
  if (!removed) {
    return json({ error: "not_found", error_description: "No such device." }, 404);
  }
  return json({ revoked: deviceId });
}

/** Reports whether a connection has a credential, without returning it. */
export async function handleHeadCredential(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
  connection: string,
): Promise<Response> {
  const caller = await authenticateDevice(request, env, ctx);
  if (!caller) return new Response(null, { status: 401 });
  const record = await getCredentialRecord(env.HUB_KV, connection);
  return new Response(null, { status: record ? 204 : 404 });
}
