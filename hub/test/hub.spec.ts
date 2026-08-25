/**
 * Tests for the hub's door and its credential store.
 *
 * Three things matter enough to test here, and they are the three the brief
 * named:
 *
 *   1. The device flow issues a token, and only after a human approves.
 *   2. Reading a credential requires a device token.
 *   3. Writing a credential requires a fresh approval, and a device token
 *      does not help.
 *
 * The requests go through the exported Worker, so the OAuth provider wrapper
 * is in the path exactly as it is in production.
 */
import { env, createExecutionContext, waitOnExecutionContext } from "cloudflare:test";
import { describe, expect, it } from "vitest";

import worker from "../src/index.js";
import type { Env } from "../src/env.js";

const ORIGIN = "https://hub.test";
const DEV_TOKEN = "test-dev-signin-token";

/** Sends one request through the Worker and waits for its background work. */
async function call(request: Request): Promise<Response> {
  const ctx = createExecutionContext();
  const response = await worker.fetch(request, env, ctx);
  await waitOnExecutionContext(ctx);
  return response;
}

/** Runs the whole device flow and returns a usable device token. */
async function loginDevice(name: string): Promise<string> {
  const startResponse = await call(
    new Request(`${ORIGIN}/device/code`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ device_name: name }),
    }),
  );
  expect(startResponse.status).toBe(200);
  const start = (await startResponse.json()) as { device_code: string; user_code: string };

  const approveResponse = await call(
    new Request(`${ORIGIN}/ui/device/approve?dev_token=${DEV_TOKEN}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_code: start.user_code, device_name: name }),
    }),
  );
  expect(approveResponse.status).toBe(200);

  const tokenResponse = await call(
    new Request(`${ORIGIN}/device/token`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
        device_code: start.device_code,
      }),
    }),
  );
  expect(tokenResponse.status).toBe(200);
  const token = (await tokenResponse.json()) as { access_token: string };
  return token.access_token;
}

/** Obtains an approval ticket the way a browser human does. */
async function approvalTicket(connection: string): Promise<string> {
  const response = await call(
    new Request(`${ORIGIN}/ui/approvals`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${DEV_TOKEN}`,
      },
      body: JSON.stringify({ connection }),
    }),
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as { approval_ticket: string };
  return body.approval_ticket;
}

/** Stores a credential through the real write path. */
async function storeCredential(connection: string, secret: string, identity: string) {
  const ticket = await approvalTicket(connection);
  const response = await call(
    new Request(`${ORIGIN}/creds/${connection}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ secret, identity, approval_ticket: ticket }),
    }),
  );
  expect(response.status).toBe(200);
}

describe("the device flow", () => {
  it("gives a device code, a short user code, and a verification URL", async () => {
    const response = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "macbook" }),
      }),
    );

    expect(response.status).toBe(200);
    const body = (await response.json()) as Record<string, unknown>;

    expect(typeof body.device_code).toBe("string");
    expect(body.verification_uri).toBe(`${ORIGIN}/ui/device`);
    expect(body.interval).toBe(5);
    expect(body.expires_in).toBe(900);
    // The short code is the one a person reads aloud and types.
    expect(body.user_code).toMatch(/^[BCDFGHJKMNPQRSTVWXYZ23456789]{4}-[BCDFGHJKMNPQRSTVWXYZ23456789]{4}$/);
  });

  it("withholds the token until a human approves", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "mini" }),
      }),
    );
    const start = (await startResponse.json()) as { device_code: string };

    const pollResponse = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );

    expect(pollResponse.status).toBe(400);
    const body = (await pollResponse.json()) as { error: string };
    expect(body.error).toBe("authorization_pending");
  });

  it("refuses to approve a device for a caller who is not signed in", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "attacker" }),
      }),
    );
    const start = (await startResponse.json()) as { device_code: string; user_code: string };

    // No dev_token, so no human identity: the approval page must not act.
    const approveResponse = await call(
      new Request(`${ORIGIN}/ui/device/approve`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_code: start.user_code }),
      }),
    );
    expect(approveResponse.status).toBe(401);

    // And the machine still gets nothing.
    const pollResponse = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(((await pollResponse.json()) as { error: string }).error).toBe("authorization_pending");
  });

  it("issues a token once a human approves, and only once", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "grumpyorange" }),
      }),
    );
    const start = (await startResponse.json()) as { device_code: string; user_code: string };

    const approveResponse = await call(
      new Request(`${ORIGIN}/ui/device/approve?dev_token=${DEV_TOKEN}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_code: start.user_code, device_name: "grumpyorange" }),
      }),
    );
    expect(approveResponse.status).toBe(200);

    const first = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(first.status).toBe(200);
    const issued = (await first.json()) as { access_token: string; device_name: string };
    expect(issued.access_token).toMatch(/^jfd_/);
    expect(issued.device_name).toBe("grumpyorange");

    // A replayed poll must not produce a second token from one approval.
    const second = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(second.status).toBe(400);
    expect(((await second.json()) as { error: string }).error).toBe("expired_token");
  });

  it("rejects a grant type it does not serve", async () => {
    const response = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ grant_type: "authorization_code", device_code: "x" }),
      }),
    );
    expect(response.status).toBe(400);
    expect(((await response.json()) as { error: string }).error).toBe("unsupported_grant_type");
  });
});

describe("reading a credential needs a device token", () => {
  it("refuses a read with no token", async () => {
    await storeCredential("slack-read-none", "xoxp-secret", "shreyans@example.com");

    const response = await call(new Request(`${ORIGIN}/creds/slack-read-none`));
    expect(response.status).toBe(401);
    expect(response.headers.get("WWW-Authenticate")).toContain("Bearer");
  });

  it("refuses a read with a token it never issued", async () => {
    await storeCredential("slack-read-bad", "xoxp-secret", "shreyans@example.com");

    const response = await call(
      new Request(`${ORIGIN}/creds/slack-read-bad`, {
        headers: { Authorization: "Bearer jfd_not-a-real-token" },
      }),
    );
    expect(response.status).toBe(401);
  });

  it("returns the secret to a device that holds a token", async () => {
    await storeCredential("slack-read-ok", "xoxp-the-real-secret", "shreyans@example.com");
    const token = await loginDevice("macbook");

    const response = await call(
      new Request(`${ORIGIN}/creds/slack-read-ok`, {
        headers: { Authorization: `Bearer ${token}` },
      }),
    );

    expect(response.status).toBe(200);
    const body = (await response.json()) as { secret: string; identity: string };
    expect(body.secret).toBe("xoxp-the-real-secret");
    expect(body.identity).toBe("shreyans@example.com");
    // The secret must not be cached anywhere on the path.
    expect(response.headers.get("Cache-Control")).toBe("no-store");
  });

  it("reports a missing connection as 404, not as a failure to authenticate", async () => {
    const token = await loginDevice("macbook-404");
    const response = await call(
      new Request(`${ORIGIN}/creds/never-stored`, {
        headers: { Authorization: `Bearer ${token}` },
      }),
    );
    expect(response.status).toBe(404);
  });

  it("stops honouring a token after the device is revoked", async () => {
    await storeCredential("slack-revoke", "xoxp-secret", "shreyans@example.com");
    const token = await loginDevice("doomed-laptop");

    const listResponse = await call(
      new Request(`${ORIGIN}/devices`, { headers: { Authorization: `Bearer ${token}` } }),
    );
    const devices = (await listResponse.json()) as {
      devices: Array<{ device_id: string; name: string }>;
    };
    const doomed = devices.devices.find((device) => device.name === "doomed-laptop");
    expect(doomed).toBeDefined();

    const revokeResponse = await call(
      new Request(`${ORIGIN}/devices/${doomed!.device_id}`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
      }),
    );
    expect(revokeResponse.status).toBe(200);

    const afterRevoke = await call(
      new Request(`${ORIGIN}/creds/slack-revoke`, {
        headers: { Authorization: `Bearer ${token}` },
      }),
    );
    expect(afterRevoke.status).toBe(401);
  });
});

describe("writing a credential needs a fresh approval", () => {
  it("refuses a write that carries no approval", async () => {
    const response = await call(
      new Request(`${ORIGIN}/creds/slack-write-none`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret: "xoxp-new", identity: "shreyans@example.com" }),
      }),
    );

    expect(response.status).toBe(403);
    expect(((await response.json()) as { error: string }).error).toBe("approval_required");
  });

  it("refuses a write that carries only a device token", async () => {
    // This is the case the whole design turns on. A device token reads every
    // credential in the hub, and it must still not be able to write one.
    const token = await loginDevice("macbook-write-attempt");

    const response = await call(
      new Request(`${ORIGIN}/creds/slack-write-device`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ secret: "xoxp-new", identity: "shreyans@example.com" }),
      }),
    );

    expect(response.status).toBe(403);
    expect(((await response.json()) as { error: string }).error).toBe("approval_required");
  });

  it("refuses an approval request from a caller who is not a signed-in human", async () => {
    const token = await loginDevice("macbook-no-approval");

    const response = await call(
      new Request(`${ORIGIN}/ui/approvals`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ connection: "slack-smarta" }),
      }),
    );

    // A device token is not a browser sign-in, so it cannot mint an approval.
    expect(response.status).toBe(401);
  });

  it("accepts a write that carries a fresh approval", async () => {
    const ticket = await approvalTicket("slack-write-ok");

    const response = await call(
      new Request(`${ORIGIN}/creds/slack-write-ok`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          secret: "xoxp-brand-new",
          identity: "shreyans@example.com",
          approval_ticket: ticket,
        }),
      }),
    );

    expect(response.status).toBe(200);
    const body = (await response.json()) as { connection: string; updated_by: string };
    expect(body.connection).toBe("slack-write-ok");
    expect(body.updated_by).toBe("development-signin");
  });

  it("spends an approval, so the same ticket cannot write twice", async () => {
    const ticket = await approvalTicket("slack-write-once");

    const first = await call(
      new Request(`${ORIGIN}/creds/slack-write-once`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret: "first", approval_ticket: ticket }),
      }),
    );
    expect(first.status).toBe(200);

    const second = await call(
      new Request(`${ORIGIN}/creds/slack-write-once`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret: "second", approval_ticket: ticket }),
      }),
    );
    expect(second.status).toBe(403);
    expect(((await second.json()) as { error: string }).error).toBe("approval_invalid");
  });

  it("does not let an approval for one connection write another", async () => {
    const ticket = await approvalTicket("slack-connection-a");

    const response = await call(
      new Request(`${ORIGIN}/creds/slack-connection-b`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret: "wrong-target", approval_ticket: ticket }),
      }),
    );

    expect(response.status).toBe(403);
    expect(((await response.json()) as { error: string }).error).toBe("approval_invalid");
  });
});

describe("the browser approval page", () => {
  it("shows a confirmation page for a signed-in human", async () => {
    const response = await call(
      new Request(`${ORIGIN}/ui/approvals?connection=slack-page&dev_token=${DEV_TOKEN}`, {
        headers: { Accept: "text/html" },
      }),
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("Content-Type")).toContain("text/html");
    const body = await response.text();
    expect(body).toContain("slack-page");
    expect(body).toContain("Approve this write");
  });

  it("refuses to show the page to a caller who is not signed in", async () => {
    const response = await call(
      new Request(`${ORIGIN}/ui/approvals?connection=slack-page`, {
        headers: { Accept: "text/html" },
      }),
    );
    expect(response.status).toBe(401);
  });

  it("mints a working ticket from the page's form submission", async () => {
    const formResponse = await call(
      new Request(`${ORIGIN}/ui/approvals?dev_token=${DEV_TOKEN}`, {
        method: "POST",
        headers: {
          Accept: "text/html",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: "connection=slack-from-form",
      }),
    );

    expect(formResponse.status).toBe(200);
    expect(formResponse.headers.get("Content-Type")).toContain("text/html");
    const shown = await formResponse.text();

    // The page must show the ticket, because a person has to copy it.
    const match = /<span class="code">([^<]+)<\/span>/.exec(shown);
    expect(match).not.toBeNull();
    const ticket = match![1]!;

    // And that ticket must actually authorise the write.
    const writeResponse = await call(
      new Request(`${ORIGIN}/creds/slack-from-form`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret: "from-the-browser", approval_ticket: ticket }),
      }),
    );
    expect(writeResponse.status).toBe(200);
  });

  it("still answers JSON to a client that is not a browser", async () => {
    // The `jf` client and curl must keep the original contract.
    const response = await call(
      new Request(`${ORIGIN}/ui/approvals`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${DEV_TOKEN}`,
        },
        body: JSON.stringify({ connection: "slack-json" }),
      }),
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("Content-Type")).toContain("application/json");
    const body = (await response.json()) as { approval_ticket: string };
    expect(typeof body.approval_ticket).toBe("string");
  });

  it("does not let the form path skip the sign-in", async () => {
    // A form submission with no identity must not mint a ticket.
    const response = await call(
      new Request(`${ORIGIN}/ui/approvals`, {
        method: "POST",
        headers: {
          Accept: "text/html",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: "connection=slack-sneaky",
      }),
    );
    expect(response.status).toBe(401);
  });
});

describe("the development sign-in survives a form submission", () => {
  // The live bug: the dev token travels as ?dev_token= in the URL, and a form
  // submission does not inherit the query string. The POST therefore arrived
  // with no identity, the approval was refused, and the devauth record stayed
  // at approved: false. Each test below covers one half of the fix.

  it("approves a device when the token is only in the form body", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "form-body-machine" }),
      }),
    );
    const start = (await startResponse.json()) as { device_code: string; user_code: string };

    // No dev_token in the URL. It is in the body, where the page put it.
    const approveResponse = await call(
      new Request(`${ORIGIN}/ui/device/approve`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          user_code: start.user_code,
          device_name: "form-body-machine",
          dev_token: DEV_TOKEN,
        }).toString(),
      }),
    );
    expect(approveResponse.status).toBe(200);

    // The approval must have persisted, which is what the live bug broke.
    const tokenResponse = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(tokenResponse.status).toBe(200);
    expect(((await tokenResponse.json()) as { access_token: string }).access_token).toMatch(/^jfd_/);
  });

  it("still refuses a device approval with no token anywhere", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "no-token-machine" }),
      }),
    );
    const start = (await startResponse.json()) as { device_code: string; user_code: string };

    const approveResponse = await call(
      new Request(`${ORIGIN}/ui/device/approve`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ user_code: start.user_code }).toString(),
      }),
    );
    expect(approveResponse.status).toBe(401);

    // And no token may be collectable afterwards.
    const tokenResponse = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(((await tokenResponse.json()) as { error: string }).error).toBe("authorization_pending");
  });

  it("refuses a device approval when the form carries a wrong token", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "wrong-token-machine" }),
      }),
    );
    const start = (await startResponse.json()) as { user_code: string };

    const approveResponse = await call(
      new Request(`${ORIGIN}/ui/device/approve`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          user_code: start.user_code,
          dev_token: "not-the-right-token",
        }).toString(),
      }),
    );
    expect(approveResponse.status).toBe(401);
  });

  it("mints an approval ticket when the token is only in the form body", async () => {
    const response = await call(
      new Request(`${ORIGIN}/ui/approvals`, {
        method: "POST",
        headers: {
          Accept: "text/html",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({
          connection: "slack-form-token",
          dev_token: DEV_TOKEN,
        }).toString(),
      }),
    );

    expect(response.status).toBe(200);
    const shown = await response.text();
    expect(shown).toContain("Write approved");
  });

  it("puts the hidden field in the device page while Access is off", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "hidden-field-machine" }),
      }),
    );
    const start = (await startResponse.json()) as { user_code: string };

    const pageResponse = await call(
      new Request(
        `${ORIGIN}/ui/device?user_code=${encodeURIComponent(start.user_code)}&dev_token=${DEV_TOKEN}`,
        { headers: { Accept: "text/html" } },
      ),
    );
    expect(pageResponse.status).toBe(200);
    const body = await pageResponse.text();
    expect(body).toContain(`name="dev_token"`);
    expect(body).toContain(DEV_TOKEN);
  });

  it("never puts the hidden field in a page once Access is on", async () => {
    // With Access on, the development sign-in does not exist, so the shared
    // secret must not appear in any rendered page. A signed-in Access request
    // is simulated by the verified-identity path in access.spec.ts; here the
    // point is only that nothing echoes a dev_token back into the HTML.
    const accessEnv: Env = {
      ...env,
      ACCESS_ENABLED: "true",
      ACCESS_TEAM_DOMAIN: "jackfield.cloudflareaccess.com",
      ACCESS_AUD: "test-audience",
      DEV_SIGNIN_TOKEN: undefined,
    };

    const ctx = createExecutionContext();
    const response = await worker.fetch(
      new Request(`${ORIGIN}/ui/approvals?connection=slack-access&dev_token=${DEV_TOKEN}`, {
        headers: { Accept: "text/html" },
      }),
      accessEnv,
      ctx,
    );
    await waitOnExecutionContext(ctx);

    // Access refuses the request outright, and the body carries no token.
    expect(response.status).toBe(401);
    const body = await response.text();
    expect(body).not.toContain(`name="dev_token"`);
    expect(body).not.toContain(DEV_TOKEN);
  });

  it("puts the hidden field in both approval forms while Access is off", async () => {
    const withConnection = await call(
      new Request(`${ORIGIN}/ui/approvals?connection=slack-hidden&dev_token=${DEV_TOKEN}`, {
        headers: { Accept: "text/html" },
      }),
    );
    expect((await withConnection.text())).toContain(`name="dev_token"`);

    // The GET form needs it too: a GET submission replaces the query string.
    const withoutConnection = await call(
      new Request(`${ORIGIN}/ui/approvals?dev_token=${DEV_TOKEN}`, {
        headers: { Accept: "text/html" },
      }),
    );
    expect((await withoutConnection.text())).toContain(`name="dev_token"`);
  });
});

describe("the /ui split, which is what Access protects", () => {
  // Cloudflare Access protects exactly one prefix: /ui. Every browser page
  // must therefore live under it, and every machine endpoint must live outside
  // it — a machine has no browser and no Access session, so Access in front of
  // a machine endpoint would lock out `jf` entirely.

  it("keeps the machine endpoints outside /ui", async () => {
    const codeResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "outside-ui" }),
      }),
    );
    expect(codeResponse.status).toBe(200);
    const start = (await codeResponse.json()) as { device_code: string };

    // The token endpoint answers outside /ui too, with no browser involved.
    const tokenResponse = await call(
      new Request(`${ORIGIN}/device/token`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          grant_type: "urn:ietf:params:oauth:grant-type:device_code",
          device_code: start.device_code,
        }),
      }),
    );
    expect(((await tokenResponse.json()) as { error: string }).error).toBe("authorization_pending");

    // And the credential endpoints keep their paths.
    await storeCredential("ui-split-cred", "secret", "one@example.com");
    const token = await loginDevice("ui-split-device");
    for (const path of ["/creds/ui-split-cred", "/status", "/devices"]) {
      const response = await call(
        new Request(`${ORIGIN}${path}`, { headers: { Authorization: `Bearer ${token}` } }),
      );
      expect(response.status).toBe(200);
    }
  });

  it("tells a machine to send its person to a /ui URL", async () => {
    const response = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "url-check" }),
      }),
    );
    const body = (await response.json()) as {
      verification_uri: string;
      verification_uri_complete: string;
    };
    expect(body.verification_uri).toBe(`${ORIGIN}/ui/device`);
    expect(body.verification_uri_complete).toContain(`${ORIGIN}/ui/device?user_code=`);
  });

  it("points every rendered form at a /ui action", async () => {
    const startResponse = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "form-action-check" }),
      }),
    );
    const start = (await startResponse.json()) as { user_code: string };

    const devicePage = await call(
      new Request(
        `${ORIGIN}/ui/device?user_code=${encodeURIComponent(start.user_code)}&dev_token=${DEV_TOKEN}`,
        { headers: { Accept: "text/html" } },
      ),
    );
    expect(await devicePage.text()).toContain(`action="/ui/device/approve"`);

    const approvalPage = await call(
      new Request(`${ORIGIN}/ui/approvals?connection=slack-action&dev_token=${DEV_TOKEN}`, {
        headers: { Accept: "text/html" },
      }),
    );
    expect(await approvalPage.text()).toContain(`action="/ui/approvals"`);
  });

  it("redirects the old browser paths so stale links still work", async () => {
    const devicePage = await call(
      new Request(`${ORIGIN}/device?user_code=BCDF-GHJK`, { redirect: "manual" }),
    );
    expect(devicePage.status).toBe(301);
    expect(devicePage.headers.get("Location")).toBe(`${ORIGIN}/ui/device?user_code=BCDF-GHJK`);

    const approvalsPage = await call(
      new Request(`${ORIGIN}/approvals?connection=slack-old`, { redirect: "manual" }),
    );
    expect(approvalsPage.status).toBe(301);
    expect(approvalsPage.headers.get("Location")).toBe(
      `${ORIGIN}/ui/approvals?connection=slack-old`,
    );
  });

  it("does not redirect a machine endpoint that shares the /device prefix", async () => {
    // A prefix rule would wrongly move these. The match must be exact.
    const response = await call(
      new Request(`${ORIGIN}/device/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_name: "no-redirect" }),
        redirect: "manual",
      }),
    );
    expect(response.status).toBe(200);
  });

  it("does not keep the old POST routes", async () => {
    // A redirect would drop the body, so an un-updated client must fail
    // loudly rather than appear to work.
    const oldApprove = await call(
      new Request(`${ORIGIN}/device/approve?dev_token=${DEV_TOKEN}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_code: "BCDF-GHJK" }),
        redirect: "manual",
      }),
    );
    expect(oldApprove.status).toBe(404);

    const oldApprovals = await call(
      new Request(`${ORIGIN}/approvals`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${DEV_TOKEN}`,
        },
        body: JSON.stringify({ connection: "slack-old-post" }),
        redirect: "manual",
      }),
    );
    expect(oldApprovals.status).toBe(404);
  });
});

describe("the credential store", () => {
  it("keeps no readable secret in KV", async () => {
    await storeCredential("slack-at-rest", "xoxp-must-not-appear", "shreyans@example.com");

    const stored = await env.HUB_KV.get("cred:slack-at-rest", "text");
    expect(stored).not.toBeNull();
    // The record is there, but the secret inside it is not readable.
    expect(stored).not.toContain("xoxp-must-not-appear");
    expect(stored).toContain("shreyans@example.com"); // the identity is not a secret
  });

  it("stores no usable device token in KV", async () => {
    const token = await loginDevice("hash-check");
    const listing = await env.HUB_KV.list({ prefix: "device:" });
    // The token is stored only as a hash, so its own text is not a KV key.
    expect(listing.keys.some((key: { name: string }) => key.name.includes(token))).toBe(false);
  });
});

describe("the status panel", () => {
  it("needs a device token", async () => {
    const response = await call(new Request(`${ORIGIN}/status`));
    expect(response.status).toBe(401);
  });

  it("lists the stored connections and their age, and admits it does not probe", async () => {
    await storeCredential("status-one", "secret-one", "one@example.com");
    const token = await loginDevice("status-reader");

    const response = await call(
      new Request(`${ORIGIN}/status`, { headers: { Authorization: `Bearer ${token}` } }),
    );
    expect(response.status).toBe(200);

    const body = (await response.json()) as {
      connections: Array<{ connection: string; identity: string; upstream_ok: null }>;
      probes_implemented: boolean;
    };

    const entry = body.connections.find((item) => item.connection === "status-one");
    expect(entry).toBeDefined();
    expect(entry!.identity).toBe("one@example.com");
    // Phase 1 does not probe upstreams, and the payload says so plainly.
    expect(entry!.upstream_ok).toBeNull();
    expect(body.probes_implemented).toBe(false);

    // No secret may appear in the status payload.
    expect(JSON.stringify(body)).not.toContain("secret-one");
  });
});

describe("the OAuth front", () => {
  it("advertises dynamic client registration, so a connector can self-register", async () => {
    const response = await call(
      new Request(`${ORIGIN}/.well-known/oauth-authorization-server`),
    );
    expect(response.status).toBe(200);

    const metadata = (await response.json()) as {
      registration_endpoint?: string;
      authorization_endpoint?: string;
      token_endpoint?: string;
    };
    expect(metadata.registration_endpoint).toBe(`${ORIGIN}/register`);
    expect(metadata.authorization_endpoint).toBe(`${ORIGIN}/authorize`);
    expect(metadata.token_endpoint).toBe(`${ORIGIN}/token`);
  });

  it("does not serve an MCP endpoint in Phase 1", async () => {
    const health = await call(new Request(`${ORIGIN}/health`));
    expect(((await health.json()) as { mcp: boolean }).mcp).toBe(false);

    const mcp = await call(new Request(`${ORIGIN}/mcp`));
    expect(mcp.status).toBe(404);
  });
});
