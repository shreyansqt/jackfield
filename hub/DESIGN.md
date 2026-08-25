# Hub design notes — Phase 1

These notes record the decisions taken while building the Phase 1 hub, and the
costs each one carries. The architecture brief of 2026-08-24 and issue #9 set
the requirements. This file explains how the code meets them, and where it does
not yet.

## 1. Where credentials are stored: encrypted KV, not the Secrets Store

**The decision: encrypted-at-rest KV, with a master key held as a deploy-time
Worker secret.**

The requirement that settles this is in issue #9: `jf auth <connection>` writes
a new credential at runtime. The hub must therefore create and read credentials
without a redeploy.

The Cloudflare Secrets Store cannot do that. Two facts rule it out, both
verified against the Cloudflare documentation on 2026-08-24:

1. **The Worker binding is read-only.** The documented API surface is
   `await env.<BINDING>.get()`. There is no `put`, `set`, `create` or `delete`
   on the binding. Writes happen only through the REST API, the wrangler CLI,
   or the dashboard.
2. **Bindings are fixed at deploy time.** Each secret needs its own entry in
   `wrangler.jsonc`, naming the exact secret:
   `{ "binding": "...", "store_id": "...", "secret_name": "..." }`. A secret
   created at runtime through the REST API is not reachable through any binding
   until somebody adds a config entry and redeploys.

A hub built on the Secrets Store would therefore have to call the Cloudflare
REST API from inside the Worker for every read and every write. That means
holding a `Secrets Store Write` API token inside the Worker, which is a
credential at least as powerful as the ones being protected, and it adds a
network round trip to every credential read. The best practice for Workers is
to use bindings instead of the REST API, and here the REST API would be the
only path.

### What encrypted KV costs

State the costs honestly:

- **The master key is a single point of failure.** `CRED_MASTER_KEY` decrypts
  every credential. If it leaks, every credential leaks. If it is lost, every
  credential is lost, because there is no recovery path. The Secrets Store
  would have spread that risk across Cloudflare's own key management.
- **Rotating the master key needs a migration.** Nothing re-encrypts the
  existing records today. A rotation must read every credential with the old
  key and write it back with the new one. That tool is not written yet.
- **KV is eventually consistent.** A credential read soon after a write can
  return the previous value. For this hub that is acceptable: writes are rare
  and a human is present, and the `jf` cache already tolerates staleness.
  Revocation is the one place where staleness would matter, and section 4
  explains how that is handled.
- **A KV reader sees metadata.** The connection names, the identities and the
  write timestamps are stored in clear text. Only the secret values are
  encrypted. Anyone with read access to the namespace learns which services the
  hub holds credentials for.

### How the encryption works

`src/crypto.ts` uses AES-GCM with a 256-bit key and a fresh 12-byte
initialisation vector per write. The connection name is passed as additional
authenticated data. That binding matters: a ciphertext copied from one
connection's KV key to another fails to decrypt, instead of quietly returning
the wrong secret to whoever asked.

Device tokens are never stored. The hub keeps only their SHA-256 hash, so
someone who reads the KV namespace cannot recover a token that still works.

### If this decision is revisited

The Secrets Store becomes the better answer if Cloudflare adds a write method
to the binding, or if the set of connections becomes fixed and small enough
that a redeploy per new connection is acceptable. Neither holds today.

## 2. KV rather than a Durable Object for the registry

The brief asked for a Durable Object or KV, with the reason written down.

**The decision: KV.**

A Durable Object buys strong consistency and single-threaded coordination. This
hub needs neither:

- The records are independent. A credential, a device token and an approval
  ticket never have to change together, so there is no transaction to protect.
- There is one user. Concurrent writes to the same record are not a real case.
- Credential reads are the hot path and they come from several machines. KV
  reads are served at the edge; every Durable Object read would travel to the
  single location that object lives in.

KV also keeps Phase 1 free of a `migrations` entry, which keeps the wrangler
config small until Phase 2 introduces the MCP agent, which does need a Durable
Object.

**The cost.** Eventual consistency, discussed above, and no transactions. One
place needed care because of it: see section 4.

## 3. Reads versus writes

This is the rule the design turns on:

- **Reading** a credential needs a device token. Agents read constantly.
- **Writing** a credential needs a fresh browser approval, every time. A device
  token is not sufficient, and it is not even a contributing factor.

The write path in `src/creds.ts` never looks for a device token. It requires an
`approval_ticket`, which only `POST /approvals` issues, and only to a caller
that `authenticateHuman` recognises. The ticket is short-lived (five minutes),
covers exactly one connection, and is deleted when it is spent — whether or not
it matched. One approval therefore permits one write, to one connection.

The secret itself never passes through the browser. `jf auth` gets the ticket
from the browser and then sends the secret directly to the hub.

`/approvals` answers in two shapes, because it has two callers. A browser gets
an HTML page: `GET /approvals?connection=<name>` names the connection and asks
the person to approve, and the form's `POST` shows the ticket as text they can
copy. A script gets the original JSON. The hub chooses by the `Accept` and
`Content-Type` headers, so the contract `jf` and curl were written against did
not change.

Both shapes go through the same `authenticateHuman` check. The HTML path is a
different presentation of the gate, not a way around it: a form submission
without a verified identity is refused exactly as a JSON call is, and a test
covers that case specifically.

## 4. Device revocation, and the bug that was found while testing

Revocation deletes two keys: the `device:<tokenHash>` record and the
`deviceindex:<deviceId>` pointer.

Testing found a real defect here, which is worth recording because it would
have been a security bug in production. A credential read schedules a
last-used-timestamp write through `ctx.waitUntil`. That write runs after the
response is sent. If a revocation landed in between, the background write
recreated the deleted `device:` record, and the revoked token kept working.

The fix makes the index key the single authority on whether a device exists:

- `revokeDevice` deletes the index key.
- `lookupDeviceByToken` accepts a token only when the index key still points at
  that token's hash. A resurrected `device:` record alone is not enough.
- `listDevices` enumerates from the index keys, so a revoked device cannot
  reappear in `jf devices`.
- `touchDevice` re-checks the index before it writes.

A revoked token therefore stops working even if a background write recreates
its record.

## 5. Why the device flow is hand-written

`@cloudflare/workers-oauth-provider` version 0.10.3 implements no device grant.
Its distributed code contains no reference to `device_code`,
`device_authorization`, or the device grant type. The brief said not to fight
the library if it turned out to be a poor fit, and this is that case.

So the two halves are separate, and each uses the right tool:

- **The browser and connector path** uses the library. It serves the metadata
  documents, the token endpoint, and dynamic client registration, which is what
  a claude.ai custom connector needs in Phase 3 to register itself.
- **The device path** is implemented in `src/device.ts`, following RFC 8628.

The credential API is deliberately **not** behind the provider's `apiRoute`. It
authenticates with device tokens, which the hub issues itself. Putting it
behind `apiRoute` would make the provider reject every device token before the
route handler ran. Phase 2 puts the MCP endpoint behind `apiRoute`, where the
connector's OAuth access token is the right credential.

## 6. Cloudflare Access, and why the header alone was not enough

The Access token is now **verified**. `src/access.ts` does the verification and
`authenticateHuman` in `src/auth.ts` calls it.

### Every browser page lives under /ui

Access protects a path prefix, so the routes are arranged to give it exactly
one prefix to protect. Every page a person opens is under `/ui`:

- `GET /ui/device` and `POST /ui/device/approve`
- `GET /ui/approvals` and `POST /ui/approvals`

Every machine endpoint stays outside it: `POST /device/code`,
`POST /device/token`, `/creds/...`, `/status` and `/devices`. This split is not
cosmetic. A machine calls those endpoints with a device token and no browser,
and it cannot complete an Access sign-in. Access in front of them would stop
`jf` working on every machine, which is the opposite of the goal.

The old browser paths, `/device` and `/approvals`, answer a 301 redirect to
their new homes so a stale bookmark still lands on the page. Only GET
redirects. An old POST returns 404, because a redirect would silently drop the
request body and an un-updated client would appear to work while doing
nothing. The redirect table matches paths exactly rather than by prefix,
because `/device` moved while `/device/code` and `/device/token` did not.

One browser page stays outside `/ui`: `/authorize`, the OAuth consent page. Its
address is published in the OAuth metadata a connector reads at registration
time, so moving it would break the connector path that Phase 3 depends on. It
is unused in Phase 1. A deployer who wants Access on it adds a second
application; the hub verifies the token on that page either way, so the page is
not unprotected, only unprotected *by Access*.

### The login method is the deployer's choice

The hub does not care which login method Access uses, because it verifies a
signed token and reads the `email` claim. One-time PIN is the cheapest option
and the one the README recommends: it needs no identity provider configuration
at all, since Access emails a code to the address in the policy. Google or any
other provider works identically from the hub's side.

Nothing about the deployer's identity is committed to this repository. The team
domain and the audience tag live in `wrangler.jsonc` and are not secrets. The
email address, the policy and the login method live in the Cloudflare
dashboard.

### The defect this closes

The earlier code read the identity from the `Cf-Access-Authenticated-User-Email`
header and only checked that `Cf-Access-Jwt-Assertion` was present. Access sets
both headers, but so can anybody else. A request that reaches the Worker
without passing through Access — over the `workers.dev` hostname, for example —
carried whatever headers its sender chose. Sending
`Cf-Access-Authenticated-User-Email: someone@example.com` and any non-empty
assertion was enough to be treated as the signed-in owner, which is enough to
mint an approval ticket and write a credential.

The correctness of the browser path therefore rested on a deployment property:
that no route to the Worker bypasses Access. That property is easy to lose and
gives no signal when it is lost.

### What is checked now

`verifyAccessToken` refuses the token unless every one of these holds:

1. The token has three base64url segments.
2. The header names `alg: "RS256"` and carries a `kid`. `none` and the `HS*`
   family are refused before any key is loaded, so a caller cannot pick the
   algorithm and supply its key.
3. The `kid` appears in the team's JWKS at
   `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`.
4. The RSASSA-PKCS1-v1_5 signature verifies over `<header>.<payload>`.
5. `aud` contains `ACCESS_AUD`, so a token minted for another Access
   application in the same team does not open this one.
6. `iss` equals `https://<team domain>`.
7. `exp` is in the future, and `nbf`/`iat` are not in the future. A 60-second
   clock skew allowance applies to all three.

**The identity comes from the verified `email` claim.** The
`Cf-Access-Authenticated-User-Email` header is now read nowhere in the hub. A
service token has no email, so `sub` stands in for it.

The token is read from the `Cf-Access-Jwt-Assertion` header, and from the
`CF_Authorization` cookie when that header is absent. The cookie value is the
same JWT and goes through the same verification, so accepting it adds no trust.

Access in front of the Worker is still the right configuration — it stops
unauthenticated traffic before it costs a request — but the hub no longer
depends on it for correctness.

### No JWT dependency

The verification is written against WebCrypto. Access signs with RS256 only, so
the work is one `crypto.subtle.verify` call plus the claim checks above. A JWT
library would add a dependency and an algorithm-negotiation surface to save
about forty lines.

### The JWKS caching choice

**In-memory, per isolate, one-hour TTL, keyed by team domain.** It is a plain
module-level `Map` in `src/access.ts`. It lives as long as the isolate and dies
with it.

- **Not KV.** A KV read costs about what the JWKS fetch it replaces costs, and
  it would put a shared, writable copy of the trust anchor into storage.
- **One hour, not longer.** Access rotates its signing keys roughly every six
  weeks and publishes each new key before it signs with it, so an hour sits far
  inside the window.
- **An unknown `kid` forces one refetch**, so a rotation takes effect at once
  rather than at the end of the TTL. This is also a way for a caller to make
  the hub fetch: a token with a random `kid` causes one subrequest to a
  Cloudflare-hosted endpoint. That reveals nothing and the request still ends
  in a 401.
- **The cost:** a cold isolate pays one JWKS fetch on its first Access request,
  and each isolate keeps its own copy. Both are acceptable for a hub with one
  user and rare browser traffic.

A JWKS fetch that fails or answers with an error refuses the caller. The hub
fails closed.

### The development sign-in

When `ACCESS_ENABLED` is not `"true"`, the hub uses a single shared secret in
`DEV_SIGNIN_TOKEN`. It exists so the flows can be exercised and tested before
Access is configured. It is not a substitute for Access, and the sign-in page
says so. Once `ACCESS_ENABLED` is `"true"`, the Access branch never reads that
secret, so leaving it set does not reopen the path.

The development token travels in the URL, and a form submission does not
inherit the query string. The approval pages therefore re-embed it as a hidden
`dev_token` field, and the POST handlers read the form before they check the
identity, because a request body can be read only once. This does put the
shared development secret into the HTML of a page that the same secret already
unlocked, which reveals it to nobody who could not already see it. The field is
never rendered once Access is on: `presentedDevToken` returns null in that
case, and a test asserts the token appears in no page then.

A live deployment found this the hard way. Before the fix, every browser
approval failed: the POST arrived unauthenticated, the page rendered a 401, and
the device-authorization record stayed at `approved: false` while the machine
polled forever.

## 7. The token format leaves room for a workspace-locked grant

Issue #9 section 6 asked only for room, not for the feature.

Device tokens carry the prefix `jfd_` and are otherwise opaque random strings,
resolved by a hash lookup into a record. The record is a JSON object. A future
workspace-locked grant adds fields to that record — a workspace name, an
expiry, the grant's issuer — without changing the token format or the wire
protocol. A different prefix can mark a different token class if PWC later
mints grants directly.

Nothing about the current format has to change to allow that. It is not built.

## 8. What Phase 1 does not do

- **No MCP endpoint.** That is Phase 2. `src/index.ts` marks the place, and
  lists the three things it needs.
- **No upstream liveness probes.** `GET /status` reports which credentials
  exist, who they act as, and how old they are. It does not ask Slack or Google
  whether a token still works. The response says so: every `upstream_ok` is
  `null` and `probes_implemented` is `false`. The issue's question, "which of
  my connections is logged out this morning", is therefore only partly
  answered.
- **No `jf` client changes.** The hub serves the endpoints; the Go CLI is not
  wired to them yet.
- **No master key rotation tool.** See section 1.
- **No Access group or policy checks.** The hub verifies who the caller is, and
  that the token was minted for this application. It does not read the `groups`
  claim or apply any policy of its own. The Access policy in the dashboard is
  what decides who may sign in, and the hub has one user.
