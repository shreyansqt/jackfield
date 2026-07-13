# jackfield — design

Status: **proposal**. Nothing is built. This documents the problem, the model, and the
decisions taken so far, with the reasoning, so that whoever builds it (human or agent)
inherits the *why* and not just the *what*.

Written 2026-07-13.

---

## 1. The problem

### 1.1 The incident

Two people share a machine and an Anthropic account. One of them connects their Gmail as
a claude.ai connector — a legitimate, useful thing to do. The other asks their agent to
schedule a meeting, expecting it to use `gog` (the Google CLI), **because that is what
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

---

## 2. The model

Three nouns. Everything else follows from them.

### Connection
A thing an agent can use. Three kinds:

- **`mcp-stdio`** — a binary/script on this machine (`stackbar`, `gog`, Breeze, 1Password).
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

---

## 3. Decisions

### 3.1 Scoped configs, not a runtime wrapper

Two ways to restrict what an agent can see:

- **(a) Generate scoped configs** — write each harness's project-level config so a
  session started in `~/work/acme` only has that workspace's servers listed.
- **(b) Launch through a wrapper** — agents start via `jf run claude`, which constructs
  the environment explicitly.

**Decision: (a).** It works with all three harnesses today, requires no runtime, and
doesn't change how you start an agent. (b) is stronger in principle but pays a constant
ergonomic tax for a marginal gain.

**Known limit of (a), stated honestly:** it cannot restrict things that don't live in a
config file you control. The **claude.ai connectors** (Slack, Figma, Gmail, Drive,
Calendar) are Anthropic-account-global — they are not in any local file, so no amount of
config generation hides them. The only way to make one invisible is to disconnect it
from the Anthropic account.

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

### 3.4 Secrets: encrypted in git (SOPS + age)

Rejected alternatives, with reasons:

- **1Password** — biometric approval on *every read* is the wrong ergonomics for
  something an agent hits constantly. These also aren't password-manager-shaped
  credentials.
- **Git, with short-lived tokens** — this was proposed and **withdrawn**. If tokens
  expire daily, then "re-auth on machine A → commit → every other machine is silently
  broken until it pulls" turns an auth problem into a distributed cache-invalidation
  problem, which is strictly worse. A cloud worker pulling a stale secret fails in a way
  that looks like a bug rather than an expired token.

**Decision: SOPS + age, secrets encrypted in the repo — *conditional on §3.3*.** The
objection above was correct, and it dissolves once tokens are long-lived. Then:

- `sops exec-env secrets.enc.yaml '<command>'` injects secrets as env vars into the
  child process. **No approval prompt, ever.** They vanish when the process exits.
- **Multiple age recipients**: laptop, Mac mini, and cloud worker each get their own
  key. No shared secret.
- Replication is `git clone` + one bootstrap step: the age private key, transported
  out-of-band. That single manual step replaces N per-service re-authentications.
- Gotchas: `.sops.yaml` `creation_rules` apply at *encryption* time — adding a recipient
  later needs `sops updatekeys` on existing files. Prefer `SOPS_AGE_KEY_FILE` over an
  inline `SOPS_AGE_KEY` on cloud workers (process lists and logs leak).

### 3.5 CLI, not a GUI

A macOS menu-bar app was considered and rejected — the same reasoning that killed the
PWC menu-bar app. A Mac-only UI excludes the Mac mini and cloud workers, which are
exactly the machines that need this most.

**Decision: a CLI (`jf`) plus skills**, so agents can query and manage connections the
way they use `pwc` today. A UI can come later if the CLI proves the model.

### 3.6 Not a gateway

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

---

## 4. What cannot be solved

State these plainly rather than discovering them mid-build.

1. **Local stdio servers are binaries.** They must exist on the machine that runs them.
   No config sync puts `stackbar`, `gog`, Breeze, or the 1Password app on a Cloudflare
   Worker. And even bridged, the semantics don't survive: `stackbar` on the Mac mini
   controls *the Mac mini's* services.
2. **claude.ai connectors are not ours to manage.** Anthropic-managed, tied to the
   claude.ai subscription, and explicitly **not loaded** when an API key is active. They
   will never appear in Codex or opencode, or in a cloud worker using an API key. The
   only lever is *connect* or *disconnect*.
3. **There is no device-code flow for MCP.** Not in the spec, not in any of the three
   harnesses. **Static tokens are the only headless path.** Do not plan around anything
   else.
4. **One bootstrap secret per machine is irreducible.** The age private key (or an
   equivalent). Any encrypted-secrets scheme has this. The goal is to make it the *only*
   one.

---

## 5. Open questions

- **Slack**: replace the claude.ai connector with a self-hosted MCP server
  (`korotovsky/slack-mcp-server`, 1.7k★, accepts a static `xoxp-` token) so Codex and
  opencode get it too — and so a second person can patch in *their own* Slack under
  their own identity.
- **Google**: already handled by Breeze (own MCP) + `gog`. The claude.ai Google
  connectors should probably be **removed** — they are the ones currently showing "needs
  authentication," and removing them eliminates the Gmail-identity leak at its source.
- **Figma**: the official MCP is OAuth-only and explicitly refuses Personal Access
  Tokens; the PAT-based community alternatives are thin (single-digit stars). Leave the
  claude.ai connector in place; not worth owning.
- **Build on `amtiYo/agents`, or write our own generator?** It already generates Claude
  Code / Codex TOML / opencode JSONC from one manifest (v0.8.9, active, ~78★), but its
  secret handling is gitignored plaintext. **Test it against the three harnesses first** —
  if it holds up, jackfield shrinks to "identity + workspace scoping + CLI profiles +
  SOPS" layered on top, which is a weekend rather than a month.
- **Does anything else belong on the panel?** Env-var profiles, per-workspace `.env`,
  API keys for non-MCP services?

---

## 6. Prior art

- **`amtiYo/agents`** — one manifest → many harness configs. Closest existing thing.
  Secrets are gitignored plaintext, no encryption.
- **chezmoi + templates** — the hand-rolled version of the same idea; composes naturally
  with SOPS.
- **MCP gateways** (MetaMCP 2.5k★ active; mcp-hub 502★ dormant since Oct 2025 and with
  **no client auth at all** — anything reaching its port gets every server behind it).
- **`direnv`**, **`aws --profile`**, **`gh auth switch`** — per-context credentials,
  solved for shells and individual CLIs, not for agents.

None of them bind **identity** to **workspace** for an agent. That's the gap.
