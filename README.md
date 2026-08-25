# jackfield

**One panel for every connection your agents use.**

A *jackfield* (or patchbay) is the panel in a studio or a telephone exchange where every
input and output terminates in one place. Instead of rewiring gear at the back of a
rack, you go to the panel and patch a short cable: *this source* → *that destination*.

That is what this is, for AI coding agents. Every MCP server, every CLI credential,
every identity lands in one place — and you decide, per workspace, what is patched to
what.

> **Status: architecture decided 2026-08-24. The hub build has not started. Two local
> pilots are running.** See [docs/design.md](docs/design.md) for the design, and §3.7 for
> the decision record that made jackfield a hub.

## The problem

If you run more than one agent harness, you already know it:

- **You authenticate the same tool over and over.** Claude Code, Codex, and opencode
  each keep their own MCP config *and their own OAuth token store*. Authenticating
  Atlassian in one does nothing for the others — on the same machine. Multiply by
  every machine you own.
- **Tokens expire constantly**, so it's not a one-time cost. It's a chore, forever.
- **You can't tell which identity a tool will act as.** This is the one that actually
  hurts. An agent with two ways to reach Gmail will pick one. Which one?

## The incident this exists to prevent

A real one, and the reason this project exists.

Two people share a machine and an Anthropic account. One of them connects their Gmail
as a claude.ai connector — a legitimate, useful thing to do. The other asks their agent
to schedule a meeting, expecting it to use [gog](https://github.com/openclaw/gogcli), a Google CLI, because **that is what
their instruction files say to use.**

The agent looked at its available tools, saw a Gmail connector, and used it.
**The meeting went out from the wrong person's identity.**

Nothing was violated, because nothing was *enforced*. An instruction file is a
suggestion to a model, not a constraint on a system. The tool was there, so it got
used.

That is **ambient authority**: every tool an agent *can* see, it *may* use — and today,
what it can see is whatever accumulated across four config files and a handful of
credential stores. Nobody chose. It just resolved.

## What jackfield does

jackfield is a **hub**: one service that holds your credentials and hands out tools.
You deploy it yourself, into your own Cloudflare account. Nobody hosts it for you.

- **One place for authentication.** Every credential lives in the hub, tagged with the
  identity it acts as and the workspaces it belongs to. You authenticate a service once,
  and every machine sees it on its next call.
- **Tools over MCP.** The hub serves Slack, Google, and proxies for remote MCP servers
  (Atlassian, Sentry, Intercom). Every tool name carries its identity — `slack (smarta)`,
  `gmail (shreyansqt@gmail.com)` — so a model choosing between two Gmails has
  information instead of a coin flip.
- **Credentials for CLIs.** `gog`, `wrangler` and `aws` still run on your machine. The
  `jf` shims fetch the credential from the hub at launch and cache it briefly. The hub
  is the authority; the machines are caches.
- **Scoped per workspace, enforced on every call.** An agent inside a workspace
  directory gets only that workspace's connections. An agent outside every workspace —
  a coordinator, or claude.ai chat — gets all of them, labelled. AWS always requires you
  to name staging or production.
- **No single master key.** A browser login for humans, an account-level connector for
  Claude surfaces, and a per-device token from `jf login` for machines. Device tokens
  are listable and revocable one at a time. Reading a credential needs a device token;
  writing one needs a fresh browser login.
- **Reachable from a cloud sandbox.** A cloud agent session gets the same tools as your
  laptop, because it asks the hub rather than reading a local file.

### What it costs

Stated plainly, because these are real:

- The hub is a **single point of failure for tool calls**. If it is down, MCP tools are
  down on every machine. CLI work keeps going only while the local cache is fresh.
- **Your secrets live in your own Cloudflare account** — including any client tokens you
  put there.
- Every tool call takes **one extra network hop**.
- **Workspace signals are claimed, not proven.** A client tells the hub where it is
  running, and the hub believes it. This prevents accidents. It is not a hard boundary
  against a malicious process on your own machine.

### What stays out of the hub

- **Machine-bound stdio servers.** A service manager controls the machine it runs on.
- **CLI execution itself.** The command runs where the work is.
- **claude.ai-native connectors.** Figma stays an Anthropic connector.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh | sh
```

That is the whole install. It needs no Go toolchain, no clone, and no `sudo`.

The script reads `uname` to pick the build for this machine, downloads the latest
release from GitHub, checks the download against the published SHA-256 checksum, and
puts the `jf` binary in `~/.local/bin`. It refuses to install a binary whose checksum
does not match. It prints a `PATH` line only when `~/.local/bin` is not on `PATH`
already. Run it again at any time to upgrade in place.

Builds ship for macOS and Linux, on both Intel and ARM.

Then point `jf` at your hub and sign this machine in:

```sh
export JF_HUB=https://your-hub.workers.dev
jf login
jf status
```

Put the hub in `~/.config/jackfield/jackfield.yaml` as a `hub:` key to set it for
every shell rather than one. See [docs/cli-gate.md](docs/cli-gate.md) for both.

### Read the script before you pipe it

Piping a script from the network into `sh` runs code you have not read. If you would
rather look first, or want a specific version:

```sh
curl -fsSL -o install.sh https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh
less install.sh
sh install.sh
```

`JF_VERSION=v1.2.3` installs one release rather than the latest, and
`JF_INSTALL_DIR` changes where the binary lands.

### The other ways

- **Download it yourself.** Every release on the
  [releases page](https://github.com/shreyansqt/jackfield/releases) carries a
  `jf_<os>_<arch>.tar.gz` archive and a `checksums.txt`. Unpack `jf` anywhere on
  `PATH`.
- **Build from source.** With a Go toolchain:
  ```sh
  go install github.com/shreyansqt/jackfield/cmd/jf@latest
  ```
- **The CLI shims are a separate, optional step.** `scripts/install-cli-shims.sh`
  makes `gog`, `wrangler`, and `aws` run through the workspace gate. Only a machine
  that gates those CLIs needs it. See [docs/cli-gate.md](docs/cli-gate.md).

## Current pilots

Both pilots keep running until the hub replaces them. Nothing is torn down before its
replacement works.

- `jackfield-gate` checks workspace data on every shared MCP tool call. The first
  profile protects the Smarta Slack server. Its findings — MCP roots for Claude Code and
  OpenCode, the Codex `sandbox-state-meta` signal, and the `tools/list` limitation — go
  straight into the hub's gate logic. See
  [docs/workspace-gate-experiment.md](docs/workspace-gate-experiment.md).
- `jf run` starts an approved CLI with one workspace profile. It removes ambient
  credential variables and rejects command flags that can replace the selected
  identity. See [docs/cli-gate.md](docs/cli-gate.md).

These gates are local safety controls. A malicious process under the same macOS user
can bypass them. A hard security boundary needs a trusted launcher or separate system
users.

## Where it is going

- **Phase 0 — now.** Fix the weekly Google re-authentication, and let `gog auth` run
  through the CLI gate for its pinned identity.
- **Phase 1.** Build the hub as a credential store, with the full login door. Exit
  condition: you authenticate once, and a second machine needs zero action.
- **Phase 2.** Move the MCP gate into the hub. Slack first, then Google, then the
  remote-server proxies.
- **Phase 3.** Add the claude.ai custom connector, then disconnect the claude.ai Google
  and Slack connectors — which closes the Gmail incident at its source.

## License

MIT (intended — see design doc).
