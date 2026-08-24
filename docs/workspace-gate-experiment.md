# Workspace gate experiment

This experiment tests workspace checks on a shared MCP server.

The current pilot connects to the `smarta` Slack profile. Claude Code, Codex and
OpenCode now use the gated endpoint for `slack-smarta`. Existing stdio Slack sessions
continue to run until those sessions end.

## Decision under test

The gate identifies the calling workspace before each tool call.

- Claude Code and OpenCode supply MCP roots. The gate calls `roots/list`.
- Codex supplies `codex/sandbox-state-meta` with a `sandboxCwd` file URI.
- The gate resolves path links and compares full path segments.
- The gate denies a call when the client supplies no supported workspace signal.
- The gate denies all roots when one root is outside the selected profile.
- The gate rejects non-local host names and browser origins.

The server advertises the Codex experimental capability during MCP initialization.
This advertisement tells Codex to attach its sandbox data to tool calls.

## Run it

Use the `jackfield-slack-smarta` StackBar service. Its gated MCP endpoint is:

```text
http://127.0.0.1:8181/mcp
```

The current policy is in `gate.example.json`. The service selects the `smarta` profile.
It permits paths under `/Users/shreyans/workspaces/smarta`.

StackBar starts two processes:

- The Slack MCP server listens on `127.0.0.1:18182`.
- The Jackfield gate listens on `127.0.0.1:8181`.

The Slack process gets its existing `smarta` token through the current dotfiles
wrapper. The repository stores no Slack token.

The other Slack profiles still use their existing stdio wrappers.

Run the short-lived checks with:

```sh
go test ./...
```

The checks cover these cases:

- An allowed Claude or OpenCode root succeeds.
- A root outside the profile fails.
- An allowed Codex sandbox path succeeds.
- The MCP handshake advertises the Codex capability.

The live pilot also passed these checks:

- Codex completed a read-only `channels_me` call from the Smarta workspace.
- Claude Code completed the same read-only call.
- OpenCode completed the harmless workspace probe.
- A call from the Jackfield path failed before Slack received it.

OpenCode uses an external OpenRouter model. The pilot did not send a private Slack
result to that model. It used the local probe to verify OpenCode roots.

## Security limit

This gate prevents an ordinary agent from using the wrong workspace profile. It also
prevents accidental cross-workspace calls.

It is not a hard boundary against a malicious local process. A process under the same
macOS user can forge MCP roots or Codex metadata. It can also call a local endpoint
directly.

A hard boundary needs a trusted launcher. The launcher can issue a short-lived token
for one workspace and one server profile. The gate can then require that token. This
design keeps the workspace decision outside the agent process.

## Failure behavior

The gate loads Slack's tool definitions when it starts. Each allowed call then goes to
the shared Slack process.

If the Slack process dies, all new Slack calls through this profile fail while it is
down. StackBar shows the service failure. Existing stdio Slack sessions are independent
during this pilot.

The gate reconnects on the next tool call after Slack restarts at the same URL. It
retries that call once. A restart can change Slack's tool definitions. The gate keeps
its old definitions until the complete StackBar service restarts.

The global harness configs can list the Slack tool definitions outside Smarta. The gate
still denies every tool call from outside Smarta. Codex does not attach workspace data
to `tools/list`, so the gate cannot hide those definitions for Codex today.

## Next test

Stop only the upstream Slack process. Start it again at the same URL. Confirm that
three connected clients recover on their next calls.

<!--
Draft: append this section to the end of docs/workspace-gate-experiment.md.
It follows the existing "Next test" section. Nothing above it changes.
-->

## Status — this pilot fed Phase 2 (2026-08-24)

The architecture session on 2026-08-24 decided that jackfield becomes a hub on
Cloudflare Workers. See `docs/design.md` §3.7 for the decision record.

This experiment is the pilot that produced the gate logic for that hub. Phase 2 ports
the logic below into the Worker.

### Findings that carry into the hub

- **MCP roots identify Claude Code and OpenCode.** The pilot verified both. The hub uses
  the same `roots/list` call.
- **Codex needs the `codex/sandbox-state-meta` signal.** The server advertises the Codex
  experimental capability during MCP initialization. Codex then attaches its `sandboxCwd`
  file URI to tool calls. The hub advertises the same capability.
- **`tools/list` filtering is not a guaranteed layer.** Codex attaches no workspace data
  to `tools/list`, so the gate cannot hide tool definitions from Codex. The claude.ai
  account connector has the same limit for a different reason: it holds one account-wide
  tool list. The hub therefore treats **call-time allow or deny** as the guaranteed
  layer, and uses identity labels in tool names so that a visible-but-denied tool is not
  confusing.
- **A missing signal must produce a decision, not a guess.** The pilot denies a call when
  the client supplies no supported workspace signal. The hub keeps this rule for device
  tokens, and adds one exception: a browser OAuth session with no roots gets the full
  labelled set, because that path proves a human is present.
- **The security limit above still applies.** Roots and `sandboxCwd` are claimed by the
  client, not proven. The hub inherits this limit exactly as written in the "Security
  limit" section.

### One change the hub makes

The pilot keys its policy on **paths** — it permits paths under
`/Users/shreyans/workspaces/smarta`. The hub keys its policy on a **workspace name**
instead. A name maps to known roots per machine **and** to repository identity (the git
remote URL), because a cloud sandbox checks out the same repository at a path we cannot
predict. This manifest change lands early in Phase 2.

### How long this pilot runs

**Until the hub reaches parity.** The hub replaces this gate for Slack first, in Phase 2.
Nothing here is torn down before the replacement works. Keep running the StackBar service
and the tests in this document until Slack calls go through the hub and pass the same
checks.
