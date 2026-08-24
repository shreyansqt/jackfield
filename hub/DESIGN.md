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

## 6. The Cloudflare Access seam

Access is **not** integrated. The brief asked for a clear seam and a config
placeholder, not the integration.

`authenticateHuman` in `src/auth.ts` is the seam. With `ACCESS_ENABLED` set to
`"true"`, it reads the identity from the `Cf-Access-Authenticated-User-Email`
header and requires `Cf-Access-Jwt-Assertion` to be present.

**What is still missing, stated plainly:** the code does not verify the Access
JWT signature against the team's public keys, and does not check the `aud`
claim against `ACCESS_AUD`. Until that lands, the browser path is secure only
if Access genuinely sits in front of the Worker and the Worker cannot be
reached by any other route. A Worker also exposed on `workers.dev` can be
called directly with a forged header. The README says the same thing in the
deployment steps.

Until Access is configured, the hub uses a development sign-in: a single shared
secret in `DEV_SIGNIN_TOKEN`. It exists so the flows can be exercised and
tested. It is not a substitute for Access, and the sign-in page says so.

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
- **No Access JWT verification.** See section 6.
