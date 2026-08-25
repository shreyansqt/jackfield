/**
 * The jackfield hub.
 *
 * One service holds every credential. Machines and agents ask it for a
 * credential over HTTPS instead of keeping their own copy. Authenticate once,
 * and every machine works.
 *
 * PHASE 1 SCOPE. This milestone builds the door and the credential store.
 * There is no MCP endpoint yet; that is Phase 2. The seam for it is marked
 * below.
 *
 * The request path:
 *
 *   OAuthProvider wraps the whole Worker. It serves the OAuth metadata, the
 *   token endpoint, and dynamic client registration by itself. Everything else
 *   falls through to `hubHandler`, which routes the hub's own endpoints.
 *
 * Why the credential API is NOT behind `apiRoute`: the credential endpoints
 * authenticate with device tokens, which the hub issues through its own device
 * flow, not with OAuth access tokens from the provider. Putting them behind
 * `apiRoute` would make the provider reject every device token before the
 * route ran. The OAuth front exists for the browser and connector path, and
 * Phase 2 will put the MCP endpoint behind it. See DESIGN.md.
 */
import { OAuthProvider } from "@cloudflare/workers-oauth-provider";

import type { Env } from "./env.js";
import { authenticateHuman } from "./auth.js";
import { html, json, page } from "./http.js";
import {
  handleApprovalPage,
  handleCreateApproval,
  handleGetCredential,
  handleHeadCredential,
  handleListDevices,
  handlePutCredential,
  handleRevokeDevice,
  handleStatus,
} from "./creds.js";
import {
  handleDeviceApprove,
  handleDeviceCode,
  handleDevicePage,
  handleDeviceToken,
} from "./device.js";

/** Routes every request the OAuth provider does not handle itself. */
const hubHandler: ExportedHandler<Env> = {
  async fetch(request, env, ctx): Promise<Response> {
    const url = new URL(request.url);
    const path = url.pathname;
    const method = request.method;

    try {
      /* ---------------- machine endpoints: the device flow ----------------
       *
       * These two stay OUTSIDE /ui. A headless machine calls them with no
       * browser and no Access session, so Cloudflare Access must not sit in
       * front of them. See the note above `hubHandler`.
       */

      if (path === "/device/code" && method === "POST") {
        return await handleDeviceCode(request, env);
      }
      if (path === "/device/token" && method === "POST") {
        return await handleDeviceToken(request, env);
      }

      /* ---------------- the browser OAuth authorize page ---------------- */

      if (path === "/authorize") {
        return await handleAuthorize(request, env);
      }

      /* ---------------- browser pages, all under /ui ----------------
       *
       * Everything a person opens in a browser lives here, so one Access
       * application protecting `/ui` covers all of it and nothing else.
       */

      if (path === "/ui/device" && method === "GET") {
        return await handleDevicePage(request, env);
      }
      if (path === "/ui/device/approve" && method === "POST") {
        return await handleDeviceApprove(request, env);
      }
      if (path === "/ui/approvals") {
        // GET shows the approval page a person uses. POST mints the ticket,
        // for both that page's form and the `jf` client.
        if (method === "GET") return await handleApprovalPage(request, env);
        if (method === "POST") return await handleCreateApproval(request, env);
        return json({ error: "method_not_allowed" }, 405, { Allow: "GET, POST" });
      }

      /* ---------------- redirects from the old browser paths ----------------
       *
       * A stale bookmark or a printed link should land on the page, not on a
       * 404. Only GET moves: a POST to an old path is a client that was not
       * updated, and a redirect would silently drop its body, so those return
       * 404 and the mistake stays visible.
       */

      if (method === "GET") {
        const moved = movedBrowserPath(path);
        if (moved) {
          const target = new URL(request.url);
          target.pathname = moved;
          return Response.redirect(target.toString(), 301);
        }
      }

      /* ---------------- the credential API ---------------- */

      const credMatch = /^\/creds\/([^/]+)$/.exec(path);
      if (credMatch) {
        const connection = decodeURIComponent(credMatch[1]!);
        if (method === "GET") return await handleGetCredential(request, env, ctx, connection);
        if (method === "HEAD") return await handleHeadCredential(request, env, ctx, connection);
        if (method === "PUT") return await handlePutCredential(request, env, connection);
        return json({ error: "method_not_allowed" }, 405, { Allow: "GET, HEAD, PUT" });
      }

      if (path === "/status" && method === "GET") {
        return await handleStatus(request, env, ctx);
      }

      /* ---------------- device management ---------------- */

      if (path === "/devices" && method === "GET") {
        return await handleListDevices(request, env, ctx);
      }
      const deviceMatch = /^\/devices\/([^/]+)$/.exec(path);
      if (deviceMatch && method === "DELETE") {
        return await handleRevokeDevice(request, env, ctx, decodeURIComponent(deviceMatch[1]!));
      }

      /* ---------------- PHASE 2 GOES HERE ----------------
       *
       * The MCP endpoint belongs at `/mcp`, served by an McpAgent from the
       * Agents SDK over streamable HTTP. It needs three things this file does
       * not have yet:
       *
       *   1. A Durable Object binding and a `migrations` entry in
       *      wrangler.jsonc for the McpAgent class.
       *   2. Wiring into OAuthProvider's `apiRoute` / `apiHandler`, so the
       *      claude.ai connector's OAuth access token guards it. The device
       *      token path stays separate and keeps working as it does now.
       *   3. The workspace scoping policy, ported from internal/gate.
       *
       * Nothing in Phase 1 blocks that. Add the route here.
       */

      if (path === "/" || path === "/health") {
        return json({ service: "jackfield-hub", phase: 1, mcp: false });
      }

      return json({ error: "not_found" }, 404);
    } catch (error) {
      // Errors are caught and reported explicitly. `passThroughOnException`
      // is deliberately not used: it would hide failures in the auth paths.
      console.error({
        message: "unhandled error in hub handler",
        path,
        method,
        error: error instanceof Error ? error.message : String(error),
      });
      return json({ error: "internal_error" }, 500);
    }
  },
};

/**
 * GET /authorize — the browser consent page for the OAuth front.
 *
 * `authenticateHuman` verifies the Cloudflare Access token when ACCESS_ENABLED
 * is "true", and falls back to the development sign-in when it is not. The
 * Access application itself is configured in the Cloudflare dashboard, in
 * front of this path. See README.md and src/access.ts.
 *
 * The page grants the full scope, because the hub has exactly one user: the
 * person who deployed it. A per-scope consent screen would be theatre here.
 */
async function handleAuthorize(request: Request, env: Env): Promise<Response> {
  const oauth = (env as Env & { OAUTH_PROVIDER?: unknown }).OAUTH_PROVIDER;
  if (!oauth) {
    return json({ error: "server_error", error_description: "OAuth provider is not bound" }, 500);
  }
  const helpers = oauth as {
    parseAuthRequest: (request: Request) => Promise<unknown>;
    lookupClient: (clientId: string) => Promise<unknown>;
    completeAuthorization: (options: unknown) => Promise<{ redirectTo: string }>;
  };

  const human = await authenticateHuman(request, env);
  if (!human) {
    return html(
      page(
        "Sign in required",
        `<h1>Sign in required</h1>
<p>This hub did not identify you.</p>
<p>${
          env.ACCESS_ENABLED === "true"
            ? "Open this page through your Cloudflare Access sign-in."
            : "Cloudflare Access is not configured yet. Use the development sign-in token."
        }</p>`,
      ),
      401,
    );
  }

  const authRequest = (await helpers.parseAuthRequest(request)) as {
    clientId: string;
    scope: string[];
  };
  const client = (await helpers.lookupClient(authRequest.clientId)) as {
    clientName?: string;
  } | null;

  if (request.method === "GET") {
    const name = client?.clientName ?? authRequest.clientId;
    return html(
      page(
        "Authorize",
        `<h1>Authorize ${escapeForPage(name)}</h1>
<p>This grants access to the jackfield hub as
<strong>${escapeForPage(human.identity)}</strong>.</p>
<form method="post">
  <p><button type="submit">Authorize</button></p>
</form>`,
      ),
    );
  }

  const { redirectTo } = await helpers.completeAuthorization({
    request: authRequest,
    userId: human.identity,
    metadata: { viaAccess: human.viaAccess },
    scope: authRequest.scope,
    props: { identity: human.identity },
  });
  return Response.redirect(redirectTo, 302);
}

/**
 * Maps an old browser path to its new home under /ui, or returns null.
 *
 * The match is exact, never a prefix. `/device` moved, but `/device/code` and
 * `/device/token` are machine endpoints that stay where they are, and a prefix
 * rule would wrongly redirect them.
 */
function movedBrowserPath(path: string): string | null {
  switch (path) {
    case "/device":
      return "/ui/device";
    case "/approvals":
      return "/ui/approvals";
    default:
      return null;
  }
}

/** Local escape helper, kept here to avoid a circular import. */
function escapeForPage(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/**
 * The OAuth front.
 *
 * `clientRegistrationEndpoint` is what lets a claude.ai custom connector
 * register itself, which it does automatically. Phase 3 connects that
 * connector; the endpoint has to exist before it can.
 *
 * The provider serves /token, /register and the metadata documents. Every
 * other path reaches `hubHandler`, including /authorize, which the provider
 * advertises but does not implement.
 */
export default new OAuthProvider({
  apiRoute: [],
  apiHandler: { fetch: () => new Response("Not found", { status: 404 }) },
  defaultHandler: hubHandler,
  authorizeEndpoint: "/authorize",
  tokenEndpoint: "/token",
  clientRegistrationEndpoint: "/register",
  scopesSupported: ["hub"],
});

export { hubHandler };
