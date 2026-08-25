# The jackfield hub

The hub holds your credentials. Your machines ask it for a credential over
HTTPS instead of each keeping its own copy. You authenticate once, and every
machine works.

You deploy this to **your own Cloudflare account**. Nobody hosts it for you,
and it holds nobody's credentials but yours.

This is Phase 1. The hub stores credentials and controls who may read and write
them. It does not serve MCP tools yet. `DESIGN.md` records the decisions and
their costs.

## What the hub does today

- It stores a credential for each connection, encrypted.
- It gives each machine its own token, through `jf login`.
- It lets a machine read a credential with that token.
- It requires a fresh browser approval before anyone writes a credential.
- It lists your machines, and revokes any one of them.

## Before you start

You need:

- A Cloudflare account.
- Node.js 20 or later.
- The repository, and a terminal in this `hub/` directory.

Install the dependencies:

```bash
pnpm install
```

Use `npm install` if you prefer npm. Note that npm 10.9.2 fails on this
dependency set with an internal error (`Cannot read properties of null`). If
you hit that, use pnpm.

## Step 1 — create the two KV namespaces

The hub uses two namespaces. The first belongs to the OAuth library. The second
holds everything the hub writes itself.

```bash
npx wrangler kv namespace create OAUTH_KV
npx wrangler kv namespace create HUB_KV
```

Each command prints an id. Open `wrangler.jsonc` and replace the two
placeholders with those ids:

```jsonc
"kv_namespaces": [
  { "binding": "OAUTH_KV", "id": "<paste the OAUTH_KV id here>" },
  { "binding": "HUB_KV", "id": "<paste the HUB_KV id here>" }
]
```

## Step 2 — create the master encryption key

This key encrypts every credential the hub stores. Generate 32 random bytes and
give them to wrangler as a secret.

```bash
head -c 32 /dev/urandom | base64 | npx wrangler secret put CRED_MASTER_KEY
```

**Keep a backup of this key somewhere safe, such as a password manager.**

- If you lose this key, you lose every credential in the hub. There is no
  recovery.
- If somebody else gets this key, they can decrypt every credential.

## Step 3 — set the development sign-in token

Cloudflare Access is not configured yet, so the hub needs a temporary way to
recognise you in a browser. Set one secret for that:

```bash
head -c 32 /dev/urandom | base64 | npx wrangler secret put DEV_SIGNIN_TOKEN
```

This is a single shared secret. It is not a substitute for Access. Step 6
replaces it.

## Step 4 — deploy the hub

```bash
npx wrangler deploy
```

Wrangler prints the URL of your Worker, for example
`https://jackfield-hub.your-subdomain.workers.dev`.

Put that URL into `wrangler.jsonc` as `HUB_ORIGIN`, then deploy once more so
the hub knows its own address:

```jsonc
"vars": {
  "HUB_ORIGIN": "https://jackfield-hub.your-subdomain.workers.dev",
  ...
}
```

```bash
npx wrangler deploy
```

Check that the hub answers:

```bash
curl https://jackfield-hub.your-subdomain.workers.dev/health
```

It replies `{"service":"jackfield-hub","phase":1,"mcp":false}`.

## Step 5 — sign a machine in

The device flow gives one machine one token. It works on a machine with no
browser, because you type the code somewhere else.

Ask the hub for a code:

```bash
curl -X POST https://YOUR-HUB/device/code \
  -H 'Content-Type: application/json' \
  -d '{"device_name":"my-laptop"}'
```

The reply holds a short `user_code`, such as `BCDF-GHJK`, and a
`verification_uri`.

Open the verification URL in a browser and type the short code. Because Access
is not configured yet, add the development sign-in token to the URL:

```
https://YOUR-HUB/ui/device?dev_token=YOUR_DEV_SIGNIN_TOKEN
```

Approve the device. Then collect the token on the machine:

```bash
curl -X POST https://YOUR-HUB/device/token \
  -H 'Content-Type: application/json' \
  -d '{"grant_type":"urn:ietf:params:oauth:grant-type:device_code",
       "device_code":"THE_DEVICE_CODE_FROM_THE_FIRST_CALL"}'
```

The reply holds an `access_token` that starts with `jfd_`. That is the device
token. Keep it on the machine. The hub shows it once and stores only its hash,
so it cannot show it to you again.

## Step 6 — put Cloudflare Access in front of the browser pages

Do this before you store a real credential.

Every page a person opens lives under one path prefix, `/ui`. One Access
application therefore covers all of them, and covers nothing else.

1. In the Cloudflare dashboard, open **Zero Trust**, then **Access**, then
   **Applications**.
2. Add a self-hosted application for `YOUR-HUB/ui`.
3. Write a policy that allows only your own email address.
4. Choose a login method. **One-time PIN needs no identity provider**: Access
   sends a code to the email address in your policy, and you type it in. Any
   other login method works too, if you already have one configured.
5. Copy two values from the application:
   - the **Application Audience (AUD) tag**, on the application's overview;
   - your **team domain**, on the Zero Trust settings page. It looks like
     `yourteam.cloudflareaccess.com`.

Then set the three variables in `wrangler.jsonc` and deploy:

```jsonc
"vars": {
  "HUB_ORIGIN": "https://YOUR-HUB",
  "ACCESS_ENABLED": "true",
  "ACCESS_TEAM_DOMAIN": "yourteam.cloudflareaccess.com",
  "ACCESS_AUD": "the audience tag you copied"
}
```

```bash
npx wrangler deploy
npx wrangler secret delete DEV_SIGNIN_TOKEN
```

Both values are required. If either is empty or wrong, the hub refuses every
browser sign-in, so a typo shows up as a 401 rather than as an open door.

**Protect `/ui` only. Do not protect the whole hostname.** Your machines call
`/device/code`, `/device/token`, `/creds/...`, `/status` and `/devices` with a
device token and no browser. Access in front of those paths would block `jf`
on every machine, because a script cannot complete an Access sign-in.

One browser page stays outside `/ui`: `/authorize`, the OAuth consent page.
Its address is fixed by the OAuth metadata that a connector reads, so moving it
would break the connector registration that Phase 3 needs. It is not used in
Phase 1. If you want Access in front of it as well, add `YOUR-HUB/authorize` as
a second application with the same policy. The hub verifies the Access token on
that page itself either way.

**No identity configuration and no secret ever goes into this repository.**
Your team domain and audience tag are not secrets, and they live in
`wrangler.jsonc`. Your email address, your login method and your policy live in
the Cloudflare dashboard. The encryption key lives in `wrangler secret put`.
Nothing about who you are is committed.

### What the hub verifies

Once `ACCESS_ENABLED` is `"true"`, the hub verifies the Access token itself on
every browser request. It checks that:

- the token is signed by your Access team's published keys, fetched from
  `https://YOUR-TEAM.cloudflareaccess.com/cdn-cgi/access/certs`;
- its `aud` matches your `ACCESS_AUD`, so a token for one of your other Access
  applications does not open this one;
- its issuer matches your team domain;
- it has not expired and is not postdated.

**Your identity comes from the verified token, not from a header.** The
`Cf-Access-Authenticated-User-Email` header is ignored. Somebody who reaches the
Worker directly and sends that header is refused.

Keep Access in front of the hub anyway. It turns unauthenticated traffic away
before it reaches your Worker. The difference is that the hub is now safe if a
request gets past it — including on the `workers.dev` address — instead of
depending on that never happening. `DESIGN.md` section 6 records the details.

## Using the hub

Replace `YOUR-HUB` with your hostname and `$TOKEN` with a device token.

### Store a credential

Writing needs a fresh browser approval every time. First get an approval
ticket.

**In a browser**, which is what `jf cred set` opens:

```
https://YOUR-HUB/ui/approvals?connection=slack-work
```

The page names the connection and asks you to approve. It then shows the
ticket, which you copy. Once Access is on, Access identifies you here. Until
then, add `&dev_token=YOUR_DEV_SIGNIN_TOKEN` to that URL.

**From a script**, the same endpoint answers JSON:

```bash
curl -X POST https://YOUR-HUB/ui/approvals \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $DEV_SIGNIN_TOKEN" \
  -d '{"connection":"slack-work"}'
```

Then write the credential with the ticket:

```bash
curl -X PUT https://YOUR-HUB/creds/slack-work \
  -H 'Content-Type: application/json' \
  -d '{"secret":"xoxp-...","identity":"you@example.com",
       "approval_ticket":"THE_TICKET"}'
```

The ticket works once, for that one connection, for five minutes.

### Read a credential

```bash
curl https://YOUR-HUB/creds/slack-work -H "Authorization: Bearer $TOKEN"
```

### See what the hub holds

```bash
curl https://YOUR-HUB/status -H "Authorization: Bearer $TOKEN"
```

This lists each connection, the identity it acts as, and how old the credential
is. It does **not** yet ask Slack or Google whether the credential still works.
Every `upstream_ok` is `null` and `probes_implemented` is `false`.

### List and revoke machines

```bash
curl https://YOUR-HUB/devices -H "Authorization: Bearer $TOKEN"
curl -X DELETE https://YOUR-HUB/devices/DEVICE_ID -H "Authorization: Bearer $TOKEN"
```

Any machine can revoke any other, including itself. That is deliberate: you
revoke a lost laptop from the machine still in your hand.

## The endpoints

| Method | Path | Who may call it |
| --- | --- | --- |
| `POST` | `/device/code` | anyone; it starts a login |
| `POST` | `/device/token` | anyone holding the device code |
| `GET` | `/ui/device` | a person in a browser |
| `POST` | `/device/approve` | a person in a browser |
| `GET` | `/ui/approvals` | a person in a browser |
| `POST` | `/ui/approvals` | a person in a browser |
| `GET` | `/creds/:connection` | a device token |
| `PUT` | `/creds/:connection` | an approval ticket, never a device token |
| `GET` | `/status` | a device token |
| `GET` | `/devices` | a device token |
| `DELETE` | `/devices/:id` | a device token |
| `GET` | `/health` | anyone |

The OAuth library also serves `/token`, `/register` and the metadata at
`/.well-known/oauth-authorization-server`.

## Development

```bash
pnpm test        # run the tests in the real Workers runtime
pnpm typecheck   # check the types
pnpm dev         # run the hub locally
```

The tests need no Cloudflare account. They run the Worker in the Workers
runtime with local KV namespaces.

## What this phase does not do

- There is no MCP endpoint. That is Phase 2.
- `/status` does not probe the upstream services.
- The `jf` command-line tool does not call the hub yet.
- There is no tool to rotate the master encryption key.
- The hub applies no Access policy of its own. It verifies who you are and that
  the token was minted for this application. Who may sign in is decided by the
  Access policy you write in the dashboard.
