# jackfield

**One panel for every connection your agents use.**

A *jackfield* (or patchbay) is the panel in a studio or a telephone exchange where every
input and output terminates in one place. Instead of rewiring gear at the back of a
rack, you go to the panel and patch a short cable: *this source* → *that destination*.

That is what this is, for AI coding agents. Every MCP server, every CLI credential,
every identity lands in one manifest — and you decide, per workspace, what's patched
to what.

> **Status: design.** Nothing is built yet. See [docs/design.md](docs/design.md) for
> the problem, the model, and the plan.

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

- **One manifest.** Every MCP server and CLI credential in one place, each tagged with
  **which identity it acts as** and **which workspaces it belongs to**.
- **Generates the native config** for Claude Code, Codex, and opencode — so an agent
  started in a workspace sees exactly that workspace's connections, and nothing else.
- **CLI credential profiles.** `wrangler` in the client workspace uses the client's
  Cloudflare account; in your own, your own. No more silently deploying to the wrong
  account.
- **Long-lived tokens** where the service offers them, so the daily re-auth chore stops
  existing.
- **Replicable.** Set up a new machine, or a headless cloud worker, without
  re-authenticating twelve servers by hand.

## What it is not

Not a gateway. Not a proxy. Nothing sits in the request path — your agents talk to
their MCP servers directly, exactly as they do today. jackfield decides *what is
plugged in*, then gets out of the way.

## License

MIT (intended — see design doc).
