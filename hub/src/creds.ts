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
import { json } from "./http.js";
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
 * POST /approvals — a human in a browser approves one credential write.
 *
 * The ticket this returns is short-lived and covers exactly one connection.
 * `jf auth` opens this in a browser, collects the ticket, and then sends the
 * PUT. The secret itself never passes through the browser.
 */
export async function handleCreateApproval(request: Request, env: Env): Promise<Response> {
  const human = await authenticateHuman(request, env);
  if (!human) {
    return json(
      {
        error: "unauthorized",
        error_description:
          "Only a person signed in through the browser can approve a credential write.",
      },
      401,
    );
  }

  const body = (await request.json().catch(() => null)) as { connection?: unknown } | null;
  if (!body || typeof body.connection !== "string" || body.connection.length === 0) {
    return json({ error: "invalid_request", error_description: "connection is required" }, 400);
  }

  const ticket = await createApproval(env.HUB_KV, body.connection, human.identity);
  return json({ approval_ticket: ticket, connection: body.connection }, 200, {
    "Cache-Control": "no-store",
  });
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
