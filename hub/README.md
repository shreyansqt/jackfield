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
https://YOUR-HUB/device?dev_token=YOUR_DEV_SIGNIN_TOKEN
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

1. In the Cloudflare dashboard, open **Zero Trust**, then **Access**, then
   **Applications**.
2. Add a self-hosted application for your hub's hostname.
3. Protect these paths: `/authorize`, `/device`, `/device/approve` and
   `/approvals`.
4. Add Google as the identity provider, and write a policy that allows only
   your own email address.
5. Copy the application audience tag and your team domain.

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

**Read this before you rely on it.** The hub reads the identity headers that
Access sets. It does not yet verify the Access token's signature. Access must
therefore really sit in front of every request that reaches the Worker. If your
Worker also answers on its `workers.dev` address, somebody can call it directly
and send those headers themselves. Put the hub on a hostname that Access
protects, and turn off the `workers.dev` route. `DESIGN.md` section 6 explains
what is still missing.

## Using the hub

Replace `YOUR-HUB` with your hostname and `$TOKEN` with a device token.

### Store a credential

Writing needs a fresh browser approval every time. First get an approval ticket
as a signed-in human:

```bash
curl -X POST https://YOUR-HUB/approvals \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $DEV_SIGNIN_TOKEN" \
  -d '{"connection":"slack-work"}'
```

Once Access is on, you make this call from the browser instead, and Access
identifies you.

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
| `GET` | `/device` | a person in a browser |
| `POST` | `/device/approve` | a person in a browser |
| `POST` | `/approvals` | a person in a browser |
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
- The Access token signature is not verified. See step 6.
