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
