# jackfield — design

Status: **architecture decided 2026-08-24. The hub build has not started. Two local
pilots run today.**

This document keeps the original design and its reasoning from 2026-07-13. Section 3.7
records a dated decision that reverses §3.6. Read §3.7 before you act on §3.1, §3.4 or
§3.6 — those sections now describe history, not the plan.

Written 2026-07-13. Decision record added 2026-08-24.

---

## 1. The problem

### 1.1 The incident

Two people share a machine and an Anthropic account. One of them connects their Gmail as
a claude.ai connector — a legitimate, useful thing to do. The other asks their agent to
schedule a meeting, expecting it to use [gog](https://github.com/openclaw/gogcli) (a Google CLI), **because that is what
their instruction files say to use.**

The agent looked at the tools available to it, saw a Gmail connector, and used it. The
meeting went out **from the wrong person's identity.**

Nothing was violated, because nothing was *enforced*. An instruction file is a
suggestion to a model; it is not a constraint on a system. The tool was present, so it
was eligible.

The same failure, in a different costume, on the same day: `wrangler` holds **one global OAuth
session**, and it happened to be a client's Cloudflare account. Every wrangler command
run from any repo silently targeted *that client's* infrastructure.
`wrangler login` would have clobbered it. Nothing warned; nothing asked.

Both are the same bug: **ambient authority.** Every tool an agent can see, it may use.
What it can see is whatever happened to accumulate across four config files and a
handful of credential stores. Nobody chose. It just resolved.

### 1.2 The chore

Separately — and this is what you *feel* daily — running more than one harness means
authenticating everything more than once:

| | Claude Code | Codex | opencode |
|---|---|---|---|
| **config** | `~/.claude.json` | `~/.codex/config.toml` | `~/.config/opencode/opencode.jsonc` |
| **MCP OAuth store** | **macOS Keychain** | `~/.codex/` (file store) | its own |

Authenticating Atlassian in Claude Code does **nothing** for Codex *on the same
machine*. Multiply by every machine. And because tokens expire, it is not a one-time
setup cost — it is a recurring chore.

The macOS Keychain dependency is also a hard blocker for headless workers: a cloud
worker can never read it, no matter how you sync config.

### 1.3 Why this isn't already solved

It partly is — the *ingredients* are all mature (SOPS/age for encrypted secrets,
chezmoi for config sync, `aws --profile` for per-account CLI credentials). What's
missing is the assembly, and the reasons are structural:

- **MCP is young, and multi-harness is younger.** The pain only exists for people
  running several harnesses. The tools that do exist for cross-harness config
  generation are months old with double-digit star counts.
- **The enterprise answer exists and doesn't fit.** Companies solve this with an MCP
  gateway + SSO + a secrets manager. That is why every gateway found in research is
  enterprise-shaped: audit logs, governance, per-employee revocation. A solo dev pays
  all of the operational cost for none of the benefit.
- **Workspace-scoped *identity* appears genuinely unsolved.** Not "which tools does
  this agent have" but "**which identity does it act as**." `direnv` does this for
  shells. `aws --profile` does it per command. Nothing found does it per
  agent-workspace, enforced. That's not an oversight — it only becomes urgent when an
  autonomous agent runs unattended in a workspace and can reach the wrong account.

**"But MetaMCP exists."** It does, and it's good — it just solves a different problem.
MetaMCP is a **gateway**: it sits in the request path, holds tokens, proxies calls. That
shape is driven by *organisational* needs — audit logs, central revocation, governing
what employees can reach. It looks like company software because it is.

*(2026-08-24: the paragraph that followed here argued that jackfield is not in the
request path. Section 3.7 reverses that. What still holds is the rest of the argument:
the enterprise product is priced and shaped for governance, so it does not fit a single
person. jackfield keeps the single-tenant, self-deployed shape.)*

The reason nobody has built the second thing is not that it's hard — it's that there is
nobody to *sell* it to. A solo developer with four machines is not a market; they're a
GitHub repo. Which is precisely why this one is open source.

---

## 2. The model

**This section survives the 2026-08-24 decision unchanged.** The three nouns were always
the good part. The hub in §3.7 implements them; it does not replace them.

Three nouns. Everything else follows from them.

### Connection
A thing an agent can use. Three kinds:

- **`mcp-stdio`** — a binary or script on this machine
  ([stackbar](https://github.com/shreyansqt/stackbar), [gog](https://github.com/openclaw/gogcli),
  the 1Password app, or anything you've built yourself).
- **`mcp-http`** — a remote MCP server (`mcp.atlassian.com`, `mcp.sentry.dev`).
- **`cli`** — a command-line tool whose credentials we manage (`gh`, `wrangler`, `aws`).
  Not MCP at all, but it has exactly the same identity problem, so it belongs on the
  same panel.

### Identity
**Who a connection acts as.** This is the first-class concept, and the reason the
project exists. `atlassian` is not one connection — it is `atlassian@client` and
possibly `atlassian@personal`. `wrangler` is not one connection — it is
`wrangler@personal` (your own Cloudflare) and `wrangler@client` (a client's).

An identity is *displayed*, always. A tool called "Gmail" tells you nothing; a tool
called **"Gmail (someone-else@…)"** tells you everything. A model choosing between `gog`
(yours) and `Gmail (someone-else@…)` has information instead of a coin flip.

### Workspace
A top-level body of work (`~/work/acme`, `~/work/side-projects`) — the same unit PWC
uses. A workspace **declares which connections it patches in**. An agent started there
gets exactly those.

**Amendment, 2026-08-24:** a workspace is a **name**, not a path. The name maps to two
kinds of evidence: known roots per machine, and repo identity (the git remote URL) for
checkouts at arbitrary paths. Cloud sandboxes check out the same repository under a path
we cannot predict, so a path-keyed manifest cannot recognise them. This is a manifest
change and it lands early in Phase 2. See §3.7.4.

### 2.1 The manifest, concretely

Illustrative, not final — but the shape has to be real enough to argue with.

```yaml
identities:
  personal:   { label: "you@personal" }
  acme:       { label: "you@acme-corp" }
  housemate:  { label: "someone-else@example.com" }   # a second human on this machine

connections:
  jira:
    kind: mcp-http
    url: https://mcp.atlassian.com/v1/mcp
    identity: acme
    auth:
      header: Authorization
      value: "Basic ${ATLASSIAN_ACME_BASIC}"      # from SOPS, injected at launch

  sentry:
    kind: mcp-http
    url: https://mcp.sentry.dev/mcp
    identity: acme
    auth:
      header: Authorization
      value: "Sentry-Bearer ${SENTRY_ACME_TOKEN}"

  slack-personal:
    kind: mcp-stdio
    command: slack-mcp-server
    identity: personal
    env: { SLACK_MCP_XOXP_TOKEN: "${SLACK_PERSONAL_TOKEN}" }

  slack-housemate:                                 # same server, different human
    kind: mcp-stdio
    command: slack-mcp-server
    identity: housemate
    env: { SLACK_MCP_XOXP_TOKEN: "${SLACK_HOUSEMATE_TOKEN}" }

  wrangler:
    kind: cli
    identity: personal
    env:
      CLOUDFLARE_API_TOKEN: "${CF_PERSONAL_TOKEN}"
      CLOUDFLARE_ACCOUNT_ID: "${CF_PERSONAL_ACCOUNT}"

workspaces:
  ~/work/acme:
    patch: [jira, sentry, wrangler@acme]
  ~/work/side-projects:
    patch: [slack-personal, wrangler]              # jira/sentry NOT patched in here
```

Three things this makes concrete:

- **The same server appears twice under different identities** (`slack-personal`,
  `slack-housemate`). That is not a special case — it is the normal case on a shared
  machine, and it is why identity cannot be a field bolted onto a connection later.
- **A workspace patches in a subset.** An agent in `side-projects` has no Jira. Not
  "discouraged from using Jira" — *does not have it*.
- **`wrangler@acme` vs `wrangler`** is the whole CLI-identity problem in one line: the
  same binary, a different account, chosen by where you're standing.

*(2026-08-24: two parts of this sketch change. The `workspaces:` keys become names, each
holding a list of roots and repository URLs, not bare paths. The `${...}` values name
secrets in the hub's store instead of SOPS variables. The rest of the shape holds.)*

### 2.2 More than one human on a machine

Not an edge case — it is the situation that produced the motivating incident, and it
must work.

Two people share a machine and an Anthropic account. Both legitimately need their own
Slack, their own mail. The design must let the second person **patch in their own
connections under their own identity**, and must make it obvious to a model (and to a
human reading a tool list) which is which. The failure mode to avoid is not "the second
person has access" — it is "nobody can tell whose account a tool will act as."

This is why identity is a *display* property, not just a routing one.

*(2026-08-24 scope note: jackfield is for one person today. The published form is
single-tenant and self-deployed — one hub per person, in their own Cloudflare account.
The earlier requirement that non-engineers on a team must be able to use it is dropped.
The case above stays, because it is the incident; it is now solved by two separate hubs,
or by one hub with two labelled identities, not by a shared service.)*

---

## 3. Decisions

Sections 3.1 to 3.6 are the 2026-07-13 decisions. Section 3.7 is the 2026-08-24 decision
record. Where they conflict, §3.7 wins, and it says why.

### 3.1 Scoped configs, not a runtime wrapper

Two ways to restrict what an agent can see:

- **(a) Generate scoped configs** — write each harness's project-level config so a
  session started in `~/work/acme` only has that workspace's servers listed.
- **(b) Launch through a wrapper** — agents start via `jf run claude`, which constructs
  the environment explicitly.

**Decision: (a).** It works with all three harnesses today, requires no runtime, and
doesn't change how you start an agent. (b) is stronger in principle but pays a constant
ergonomic tax for a marginal gain.

**Pilot update, 2026-08-20:** workspace config alone cannot share one Slack process
across several agent sessions. The Slack pilot therefore uses a small runtime gate.
The CLI pilot also uses `jf run`, because native global credential stores do not enforce
workspace identity. These pilots test the stronger option before the design changes.

**Superseded, 2026-08-24:** the design did change. Both pilots pointed the same way, and
§3.7 follows them. Config generation survives as `jf sync`, which now writes one hub
endpoint per harness instead of a scoped server list.

**Known limit of (a), stated honestly:** it cannot restrict things that don't live in a
config file you control. The **claude.ai connectors** (Slack, Figma, Gmail, Drive,
Calendar) are Anthropic-account-global — they are not in any local file, so no amount of
config generation hides them. The only way to make one invisible is to disconnect it
from the Anthropic account. *(This limit is real and it stays. The plan is now to reach
Slack and Google parity in the hub and then disconnect those two connectors — see
§3.7.6.)*

### 3.2 Visibility over lockdown

The first instinct after the Gmail incident was to make the wrong tool *impossible*.
That was wrong, and it was corrected: **the second person should be able to connect their
Gmail.** It's legitimate and useful. The failure wasn't that their connector existed —
it's that nothing made it obvious **which identity a tool would act as**, so the model
picked wrong and nobody noticed until the meeting went out.

So the principle is:

> **Make the identity of every connection visible, and default to the right one.**
> Scope where it matters; do not build a wall a legitimate user has to fight.

This is more honest about how the machine is actually used — shared, by two people with
legitimate access — and it doesn't punish anyone for using the thing.

**Still current.** The hub carries this principle into tool names: every tool the hub
serves is labelled with its identity, for example `slack (smarta)` and
`gmail (shreyansqt@gmail.com)`. Labels also carry the weight when a tool is visible but
denied — see §3.7.4.

### 3.3 Long-lived tokens are the real fix for the re-auth chore

The daily-logout pain is not a sync problem. It's an *expiry* problem, and it has a
direct fix: **all four painful services offer long-lived static tokens.**

| service | static credential |
|---|---|
| **Atlassian** | API token → `Authorization: Basic base64(email:token)` *(needs an org admin to enable API-token auth for the MCP server)* |
| **Sentry** | User Auth Token → `Authorization: Sentry-Bearer <token>` (deliberately **not** `Bearer`) |
| **Intercom** | Access Token → `Authorization: Bearer <token>` |
| **Slack** | `xoxp-` user token (long-lived, not deprecated) |

And every harness can inject a header from an env var or a helper:

- **Claude Code** — `${VAR}` expansion *and* **`headersHelper`** (a shell command whose
  stdout is a JSON object of headers, resolved at connect time — tokens never touch
  disk). Requires ≥ v2.1.195.
- **Codex** — `env_http_headers = { "Authorization" = "SOME_ENV_VAR" }`. No string
  interpolation; the env-var fields only. Put the **whole** header value in the var,
  since `bearer_token_env_var` hardcodes a `Bearer ` prefix that Sentry and Atlassian
  don't use.
- **opencode** — `{env:VAR}` substitution in `url`, `headers`, `environment`, `command`.

**Once tokens are long-lived, the "keep N machines in sync" problem mostly evaporates** —
a rotation becomes a yearly event, not a daily one. This is what makes the storage
decision (below) viable.

**Still current, with one correction.** These are still the right credentials, and the
hub is where they live. The claim that expiry was fully designed out was too strong: the
Google refresh token expired every 7 days, because its Google Cloud project sat in
"Testing" publishing status. That is a configuration fault, not an OAuth law, and Phase 0
fixes it — see §3.7.7. The harness header mechanics stay documented, because `jf sync`
still writes them.

### 3.4 Secrets: encrypted in git (SOPS + age)

> **Demoted 2026-08-24 to a documented fallback.** This is no longer the design. Read it
> as the answer we would return to if the hub is unavailable or unwanted — for example
> if a deployer refuses to put secrets in a cloud account. The reasoning below is
> preserved because it is still correct on its own terms, and because §3.4b explains why
> §3.3 and §3.4 were load-bearing on each other.
>
> Issue #2 had already retracted this approach for a different reason (sharing with a
> team). That reason no longer applies, because the team requirement is dropped. The hub
> supersedes it anyway.

Rejected alternatives, with reasons:

- **1Password** — biometric approval on *every read* is the wrong ergonomics for
  something an agent hits constantly. These also aren't password-manager-shaped
  credentials.
- **Git, with short-lived tokens** — this was proposed and **withdrawn**. If tokens
  expire daily, then "re-auth on machine A → commit → every other machine is silently
  broken until it pulls" turns an auth problem into a distributed cache-invalidation
  problem, which is strictly worse. A cloud worker pulling a stale secret fails in a way
  that looks like a bug rather than an expired token.

**Original decision: SOPS + age, secrets encrypted in the repo — *conditional on §3.3*.**
The objection above was correct, and it dissolves once tokens are long-lived. Then:

- `sops exec-env secrets.enc.yaml '<command>'` injects secrets as env vars into the
  child process. **No approval prompt, ever.** They vanish when the process exits.
- **Multiple age recipients**: laptop, Mac mini, and cloud worker each get their own
  key. No shared secret.
- Replication is `git clone` + one bootstrap step: the age private key, transported
  out-of-band. That single manual step replaces N per-service re-authentications.
- Gotchas: `.sops.yaml` `creation_rules` apply at *encryption* time — adding a recipient
  later needs `sops updatekeys` on existing files. Prefer `SOPS_AGE_KEY_FILE` over an
  inline `SOPS_AGE_KEY` on cloud workers (process lists and logs leak).

**Why the hub beats it.** This approach makes every machine a replica, and a replica goes
stale. A cloud sandbox that starts, clones, and runs for twenty minutes must still
receive the age key from somewhere — the same bootstrap problem in a new shape. The hub
replaces "every machine holds a copy" with "every machine asks".

### 3.4a The surface: what `jf` actually does

The commands fall out of the model. Roughly:

```
jf status                 # the panel: every connection, its identity, is it authenticated?
jf sync                   # regenerate every harness's config for this workspace
jf auth <connection>      # (re-)authenticate one connection; write the secret back to SOPS
jf run <cmd> [args...]    # run a CLI with this workspace's identity (jf run wrangler deploy)
jf add / jf rm            # edit the manifest
```

`jf status` is the load-bearing one. The daily pain is *"which of my twelve connections
is logged out this morning?"* — and today the only way to find out is to trip over it
mid-task. A panel that shows, at a glance, **every connection, the identity it acts as,
and whether its credential is still good** is most of the value before a single config
is generated.

`jf run` is what fixes the wrangler class of bug: the identity is chosen by *where you
are*, not by whatever happens to be in a global credential store.

**Amended 2026-08-24.** The surface grows and two commands change meaning:

```
jf login                  # authenticate this machine to the hub; issues a per-device token
jf devices                # list every device token
jf devices revoke <name>  # revoke one device token
jf status                 # the panel, now read from the hub — one truth for all machines
jf sync                   # point each harness at the hub, per workspace
jf auth <connection>      # write a new secret into the hub; requires a fresh browser login
jf run <cmd> [args...]    # unchanged: run a CLI with this workspace's identity
```

### 3.4b Rotation: the workflow that has to not hurt

Even long-lived tokens expire eventually, and the flow has to be one command, not a
scavenger hunt:

1. `jf status` shows `sentry` red — credential rejected.
2. `jf auth sentry` walks you through minting a new token, writes it into the encrypted
   secrets file, and commits.
3. Every other machine picks it up on its next `git pull` + `jf sync`.

**This only works because §3.3 made the tokens long-lived.** With daily-expiring OAuth
tokens the same flow becomes a treadmill — which is exactly why the git-based approach
was rejected first and only reinstated once expiry was designed out. The two decisions
are load-bearing on each other; changing one invalidates the other.

**Replaced 2026-08-24.** Step 3 disappears. `jf auth sentry` writes the secret into the
hub, and every other machine reads the new value on its next call. No pull, no re-sync,
no window in which one machine is silently broken. Step 3 was the weakest part of the
2026-07 design, and removing it is one of the four reasons for the reversal in §3.7.

### 3.4c Why this pairs with a work coordinator (PWC)

The motivating incident is about *safety*. This section is about *leverage*, and it is
probably the stronger argument for building jackfield at all.

[PWC](https://github.com/shreyansqt/pwc) is a personal work coordinator: it holds a
durable task board, and it **dispatches each task to its own agent session** — on any
harness (claude, opencode, codex) and, over SSH+tmux, on **any machine**.

Both capabilities are, today, hollow in exactly the same way:

> PWC can place an agent anywhere. It cannot give it **hands**.

A worker dispatched to `opencode` on a remote box arrives with whatever MCP servers and
credentials happen to exist in *that machine's* config for *that harness* — which is
usually nothing, and is never workspace-aware. So in practice you dispatch to the
harness and the machine you happen to have set up by hand, which quietly defeats the
point of being harness-agnostic and remote-capable.

jackfield is the missing half:

- **PWC routes the work. jackfield provisions the connections.**
- A task in the `acme` workspace dispatched to `opencode` on a remote machine gets
  *that workspace's* Jira, *that workspace's* Sentry, and the `acme` Cloudflare identity
  — and explicitly **not** the personal ones sitting on the same disk.
- Cost routing only pays off if a cheap model on a remote box can actually *do* the work.
  It can't, if it has no tools. jackfield is what makes "route this to the cheapest
  capable model, wherever it runs" a real sentence rather than an aspiration.

The two are deliberately separate projects — a work coordinator and a connections panel
are different concerns, and jackfield must stand alone for people who don't use PWC. But
the seam between them is clean: **PWC decides *what* runs *where*; jackfield decides
*what it can reach* and *as whom*.**

**Still current, and now sharper.** The hub reaches a cloud sandbox that a config
generator cannot, so the "hands" argument is what the hub delivers. Section 3.7.5 draws
one boundary: the workspace-locked grant is PWC's problem, not jackfield's.

### 3.5 CLI, not a GUI

A macOS menu-bar app was considered and rejected — the same reasoning that killed the
PWC menu-bar app. A Mac-only UI excludes the Mac mini and cloud workers, which are
exactly the machines that need this most.

**Decision: a CLI (`jf`) plus skills**, so agents can query and manage connections the
way they use `pwc` today. A UI can come later if the CLI proves the model.

**Still current.** The hub adds one small browser surface — the OAuth authorize page and
the login approval screen — because a human must be present for those. That is a
consent screen, not an app.

### 3.6 Not a gateway

> **Reversed 2026-08-24. See §3.7.** This section is preserved as written, because it
> states the costs we now accept, and a reader must be able to see what was decided,
> when, and why it changed.

Nothing sits in the request path. Agents talk to their MCP servers **directly**, as they
do now. jackfield decides *what is plugged in*, writes the config, and gets out of the
way.

Rejected: MCP gateways/proxies (MetaMCP, mcp-hub, Docker MCP Gateway, Pomerium,
Cloudflare MCP Portals). Reasons: a single point of failure for every tool call on every
machine; an extra network hop; a service to run and patch; and the MCP spec **actively
forbids** the naive version — clients "MUST NOT send tokens to the MCP server other than
ones issued by the MCP server's authorization server," and RFC 8707 audience-binding
means a token minted for `mcp.sentry.dev` is invalid at a gateway. Gateways that do this
work by being a *separate resource server* holding its own upstream credentials — a real
pattern, but non-standard, and it buys governance benefits a solo dev doesn't need.

**The one thing a gateway can do that this cannot:** re-expose a **local stdio** server
over HTTP so a *cloud worker* can reach it. If that need ever becomes concrete, run
**MetaMCP** on the Mac mini over Tailscale as a narrow bridge for stdio servers only —
not as general architecture, and never for remote servers (a cloud worker should hit
`mcp.sentry.dev` directly, not via a hop through your house). Note MetaMCP issue #142:
upstream token auto-refresh was missing; verify before trusting it.

### 3.7 Decision record — jackfield becomes a hub (2026-08-24)

**Decision: build one service, the jackfield hub. It holds the credentials, it serves
tools over MCP, and it answers the CLI shims over HTTPS. This reverses §3.6.**

#### 3.7.1 Why the answer changed

Section 3.6 was correct for its inputs. Four inputs changed between 2026-07-13 and
2026-08-24, and each one is a thing a config generator cannot do:

1. **Full-parity cloud workers.** A cloud agent session must get the same tools as the
   MacBook. A generated config cannot install a binary or a keychain entry in a sandbox
   that lives for twenty minutes. The last paragraph of §3.6 named this as the one thing
   a gateway can do; it became the normal case rather than a corner case.
2. **Authenticate once.** One browser login must make every machine work, with zero
   action on the second machine. Generated configs move *files*, not sessions.
3. **Instant propagation.** A rotated credential must reach every reader on its next
   call. The git flow in §3.4b needed a pull on each machine, and left a window in which
   one machine was silently broken.
4. **Fix once, healed everywhere.** A fix to the gate logic must apply everywhere at
   once. With generated configs, each machine carries its own copy, and each copy drifts.

**The costs of §3.6 are real and we accept them, on purpose:**

- The hub is a **single point of failure for tool calls**. If the hub is down, MCP tools
  are down on every machine. CLI work degrades more gently, because the shims keep a
  short-lived local cache — but the cache expires.
- Tool calls take **an extra network hop**.
- It is **a service to run and patch**. Cloudflare Workers removes the server
  administration, not the responsibility.
- **Secrets live in the deployer's Cloudflare account.** For Shreyans this includes
  client (smarta) tokens in his personal Cloudflare account. He accepted this
  explicitly, and chose one hub rather than two.
- The specification objection in §3.6 stands, and we take the documented route around it:
  the hub is a **separate resource server** that holds its own upstream credentials and
  issues its own tokens. It never forwards a client token to an upstream server.

#### 3.7.2 What the hub is

- **Cloudflare Workers, TypeScript**, on the Agents SDK: `McpAgent` for the MCP server,
  Durable Objects for MCP session state, and `workers-oauth-provider` for the OAuth
  front.
- **Single-tenant and self-deployed.** One person deploys one hub into their own
  Cloudflare account. Nobody hosts anything for anyone else. No always-on personal
  machine is required.
- Secrets — the Slack `xoxp` token, Atlassian, Sentry and Intercom tokens, the Google
  OAuth refresh token, Cloudflare and AWS keys — live in that account's Workers Secrets
  or Secrets Store.

#### 3.7.3 The hub serves tools two ways

1. **An MCP endpoint** (streamable HTTP). Behind it sit the Slack tools (which replace
   the Mac mini and StackBar gate pilot), the Google tools (the hub holds the refresh
   token and calls the Google HTTP APIs), and proxies for the remote MCP servers
   (Atlassian, Sentry, Intercom). Every tool name carries its identity label:
   `slack (smarta)`, `gmail (shreyansqt@gmail.com)`.
2. **A credential API for CLIs.** `gog`, `wrangler` and `aws` still execute on the
   machine that does the work. The existing `jf` shims stay. A shim fetches the
   credential from the hub over HTTPS at launch and keeps a short-lived local cache. The
   hub is the authority; the machines are caches.

**What can never move into the hub**, stated plainly:

- **Machine-bound stdio servers.** StackBar controls the machine it runs on. Running it
  elsewhere controls the wrong machine.
- **CLI execution itself.** The command runs where the work is.
- **claude.ai-native connectors.** Figma stays an Anthropic connector; the official Figma
  MCP is OAuth-only and refuses personal access tokens.

#### 3.7.4 The door, and the scoping model

**There is deliberately no single master key.** Four ways in, each matched to who is
knocking:

- **A human in a browser** (the claude.ai connector OAuth flow, and approvals): the hub
  runs an OAuth front, and its authorize endpoint sits behind Cloudflare Access with
  Google as the identity provider. Multi-factor authentication is whatever the Google
  account enforces. claude.ai custom connectors register themselves as OAuth clients
  through dynamic client registration.
- **The claude.ai account connector**: connected **once**, at account level. Every Claude
  surface inherits it — claude.ai chat, Claude Code on a local machine, and Claude cloud
  sessions. There are no per-dispatch tokens for Claude surfaces.
- **Machines**: `jf login` issues a per-device token, through a browser flow on a normal
  machine or a device-code flow on a headless one. Tokens are listable and revocable one
  by one, with `jf devices` and `jf devices revoke <name>`.
- **Reads and writes are separated.** Agents read credentials constantly, so a device
  token is enough to read. Writing a credential is rare and a human is present, so
  `jf auth <connection>` requires a fresh browser login every time.
- **Per-dispatch, short-lived, workspace-scoped tokens** are a corner case, not a pillar.
  They exist only for cloud runtimes with no connector plumbing, such as Codex cloud —
  and whether Codex cloud supports MCP at all is still unverified.

**The scoping policy**, enforced in the hub on every call:

- An agent **inside a workspace directory** gets **only that workspace's** connections.
- An agent **outside any workspace directory** — the PWC coordinator, claude.ai chat —
  gets **all** connections, with identity-labelled names.
- **AWS keeps its no-default rule everywhere**: staging or production must be named
  explicitly.

**Workspace signals per client.** The Slack gate pilot verified the first three:

| client | signal | verified |
|---|---|---|
| Claude Code, local | MCP roots | yes, in the pilot |
| OpenCode | MCP roots | yes, in the pilot |
| Codex CLI | `codex/sandbox-state-meta` `sandboxCwd`, attached when the server advertises the Codex experimental capability | yes, in the pilot |
| claude.ai chat | none, by design | — |
| Claude cloud | roots with sandbox paths, resolved through repo identity | not yet |
| `jf` and the shims | `jf` states its own working directory over the HTTPS API | not yet |

**Two enforcement layers, and only one is guaranteed:**

- **Visibility filtering** (`tools/list`) works for roots-speaking clients only. It
  provably does not work for Codex, which attaches no workspace data to `tools/list`,
  and it does not work through the account connector, which has one account-wide tool
  list.
- **Call-time allow or deny is the guaranteed layer.** Identity labels are what keep a
  visible-but-denied tool from being confusing.

**No-signal policy, keyed to the authentication path:**

- Connector OAuth with no roots → serve the full labelled set. The browser flow proves a
  human is present.
- A device token with no workspace signal → deny.

#### 3.7.5 The honest limitation

**Roots and `sandboxCwd` are claimed, not proven.** A client asserts where it is, and
the hub believes it. This is accident prevention. It is not a hard boundary against a
malicious local process, which can forge either signal.

There is a hardening path, and we deliberately do not build it now: PWC mints a
workspace-locked grant at dispatch time, and the hub trusts the grant over the claim.
jackfield's only job today is to keep the token format able to carry such a grant later.
Shreyans placed this on PWC's side of the seam.

#### 3.7.6 What this closes

After the hub reaches Slack and Google parity, the claude.ai Google and Slack connectors
get disconnected. That closes the original wrong-identity Gmail incident at its source,
and it is the exit condition for issue #4.

#### 3.7.7 Phase 0 side-decision — gog and Google OAuth

Two faults found while diagnosing the weekly Google re-authentication:

- **Root cause of the weekly expiry:** the OAuth client's Google Cloud project sits in
  "Testing" publishing status, which expires refresh tokens after 7 days. The fix is to
  publish the personal app to production — the unverified-app warning is acceptable —
  and to make the smarta app Workspace-internal, which has no warning and no expiry.
- **The gate blocks its own credential lifecycle.** The `jf` shim hardcodes `--no-input`
  in every gog profile, so `gog auth` cannot run through the shim at all. The last
  re-authentication had to bypass the gate through `/opt/homebrew/bin/gog`. The fix
  principle: `gog auth` subcommands run interactively through the shim, but **only for
  the profile's pinned identity**. Re-authenticating a different account through the
  shim stays denied.
- **A gog fact worth recording:** gog stores tokens in the platform keyring by default,
  and its `keyring_backend` setting supports an encrypted file backend for headless
  machines. This matters for the Mac mini until the hub credential API replaces local
  storage.

#### 3.7.8 Build order

Nothing is torn down before its replacement works. Both current pilots keep running
until the hub reaches parity.

- **Phase 0 — stop the bleeding (now).** Shreyans publishes the two Google OAuth apps. A
  small Go change makes `gog auth` work through the shim, for the pinned identity only.
- **Phase 1 — the hub as a credential store.** The Worker skeleton, the full door (the
  OAuth front, Cloudflare Access with Google, `jf login` in both flows, device tokens),
  the secrets store, the `jf status` liveness panel, the `jf auth` write path, and the
  shims fetching from the hub with a cache. **Exit condition:** authenticate once; the
  MacBook and the Mac mini both work; zero action on the second machine.
- **Phase 2 — the hub as an MCP endpoint.** Port the gate logic (roots, the Codex trick,
  the no-signal policy) to the Worker. Land the manifest change to named workspaces
  early. Slack first, replacing the Mac mini pilot, then Google, then the remote-server
  proxies. `jf sync` generates harness configs that point at the hub.
- **Phase 3 — the cloud door.** Build the claude.ai custom connector. Live-test whether
  MCP roots survive the connector path for local Claude Code; if the connector strips
  them, local machines keep a direct MCP config and the connector serves only the
  browser and cloud surfaces — that is a `jf sync` detail. Investigate whether Codex
  cloud supports MCP. Then disconnect the claude.ai Google and Slack connectors.

---

## 4. What cannot be solved

State these plainly rather than discovering them mid-build.

1. **Local stdio servers are binaries.** They must exist on the machine that runs them.
   No config sync puts a local binary on a Cloudflare Worker. And even bridged, the
   semantics don't survive: a service-manager MCP running on one machine controls *that
   machine's* services, not another's. **Still true (2026-08-24).** StackBar stays
   machine-bound, and CLI execution stays where the work is.
2. **claude.ai connectors are not ours to manage.** Anthropic-managed, tied to the
   claude.ai subscription, and explicitly **not loaded** when an API key is active. They
   will never appear in Codex or opencode, or in a cloud worker using an API key. The
   only lever is *connect* or *disconnect*. **Still true.** We now pull that lever on
   purpose (§3.7.6). A custom connector to our own hub is a different thing, and it is
   ours.
3. ~~**There is no device-code flow for MCP.**~~ **Partly dissolved, 2026-08-24.** The
   statement holds for third-party MCP servers: the MCP specification defines no
   device-code flow, and none of the three harnesses implements one. It does **not** bind
   `jf login`, because we own both ends — the hub is our authorization server and `jf` is
   our client, so we implement the standard OAuth device authorization grant between
   them, as the Claude and Codex logins do. Static tokens remain the only headless path
   for a *harness* reaching a *third-party* server, and the hub removes that case by
   holding those tokens itself.
4. **One bootstrap secret per machine is irreducible.** The age private key (or an
   equivalent). Any encrypted-secrets scheme has this. The goal is to make it the *only*
   one. **Changed shape, 2026-08-24.** The hub replaces the bootstrap secret with a
   bootstrap *login*. That is better, because it is revocable per device and no key file
   travels out-of-band. It is not free: the machine still needs the hub URL, and the hub
   still needs one human who can authenticate to Cloudflare Access.
5. **The hub is a single point of failure for tool calls.** New, and accepted (§3.7.1).
6. **Workspace claims are not proofs.** New, and stated in §3.7.5.

---

## 5. Open questions

The 2026-07-13 questions about Slack, Google and the config generator are answered:
Slack and Google move into the hub, and `jf sync` writes hub endpoints rather than
generating a scoped server list, so the `amtiYo/agents` evaluation is moot. Figma stays
a claude.ai connector. These are open:

- **Does the claude.ai custom connector path preserve MCP roots for local Claude Code?**
  This decides whether local machines can use the account connector or need a direct MCP
  config. It cannot be answered from documentation; Phase 3 live-tests it.
- **Does Codex cloud support MCP at all?** If it does not, Codex cloud sessions get no
  hub tools, and the per-dispatch token corner case has no user. Verify before building
  anything for it.
- **What is the right offline behaviour for the CLI shims?** The cache makes the hub's
  downtime survivable, and it also makes revocation slower. How long should a cached
  credential stay valid, and should `jf run` fail closed or run with a stale credential
  when the hub is unreachable?
- **How does a workspace name map to evidence, exactly?** A name binds to a set of known
  roots per machine and a set of repository URLs. Two open sub-questions: what happens
  when one checkout has several remotes, and what happens when two workspaces share one
  repository.
- **Where does `jf status` get liveness from?** Showing "is this credential still good"
  needs a probe per connection. A probe costs an API call, and some services rate-limit
  them. Decide between an on-demand probe, a cached result with an age, and a scheduled
  probe in the Worker.
- **Should jackfield install local binaries too?** A manifest entry for an `mcp-stdio`
  connection names a binary that must already exist on the machine. An entry could carry
  an `install:` recipe, and `jf sync` could bring a machine up to spec. Deliberately
  deferred: it turns jackfield into a package manager, and it can never be complete,
  because a private app you built yourself has no public install recipe. **The hub
  shrinks this question**, because fewer things need to exist locally.
- **Does anything else belong on the panel?** Environment-variable profiles,
  per-workspace `.env` files, API keys for services that are not MCP at all.

---

## 6. Prior art

- **`amtiYo/agents`** — one manifest → many harness configs. Closest existing thing to
  the 2026-07 design. Secrets are gitignored plaintext, no encryption. Now moot for
  jackfield: `jf sync` writes one endpoint per harness, not a generated server list.
- **chezmoi + templates** — the hand-rolled version of the same idea; composes naturally
  with SOPS. Still the shape of the §3.4 fallback.
- **MCP gateways** (MetaMCP 2.5k★ active; mcp-hub 502★ dormant since Oct 2025 and with
  **no client auth at all** — anything reaching its port gets every server behind it).
  jackfield now occupies this category, and the difference is the point below.
- **Cloudflare Agents SDK** (`McpAgent`, Durable Objects, `workers-oauth-provider`) — the
  platform the hub is built on.
- **`direnv`**, **`aws --profile`**, **`gh auth switch`** — per-context credentials,
  solved for shells and individual CLIs, not for agents.

None of them bind **identity** to **workspace** for an agent. That is still the gap, and
the hub now enforces it per call. The gateways are the closest in *shape* and differ in
*purpose*: they govern many employees and are hosted for you, while the hub gives one
person one identity per workspace and each person deploys their own.
