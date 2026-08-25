# `jf` command reference

`jf` is the patchbay for agent credentials: workspace-scoped CLI identities, plus
the hub.

`jf` answers two questions. Which identity does a command line tool get in this
directory? And where does that credential come from? A manifest,
`jackfield.yaml`, answers the first. The hub answers the second.

This page is the written form of the built-in help. Every command here also
answers `jf help COMMAND`, and the whole tool answers `man jf`. An agent reads
[`jf schema --json`](#jf-schema) instead, which prints the same tree as JSON.

- [Getting started](#getting-started)
- [Workspace commands](#workspace-commands) — [`run`](#jf-run),
  [`resolve`](#jf-resolve)
- [Hub commands](#hub-commands) — [`login`](#jf-login), [`logout`](#jf-logout),
  [`status`](#jf-status), [`device`](#jf-device), [`cred`](#jf-cred)
- [Other commands](#other-commands) — [`schema`](#jf-schema), [`man`](#jf-man),
  [`completion`](#jf-completion), [`version`](#jf-version), [`help`](#jf-help)
- [Output and colour](#output-and-colour)
- [Files](#files)
- [Environment variables](#environment-variables)
- [When a command fails](#when-a-command-fails)
- [Renamed commands](#renamed-commands)

## Getting started

Install `jf`:

```sh
curl -fsSL https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh | sh
```

The installer puts `jf` in `~/.local/bin` and the manual page in
`~/.local/share/man/man1`. It needs no sudo and no Go toolchain.

Then sign this machine in to the hub:

```sh
export JF_HUB=https://your-hub.workers.dev
jf login
jf status
```

A machine that also gates local command line tools installs the shims. That is
the optional second step, and [docs/cli-gate.md](cli-gate.md) describes it.

## Usage

```
jf [--config PATH] COMMAND [ARGS...]
```

| Global flag | What it does |
| --- | --- |
| `--config PATH` | Read this manifest instead of searching for `jackfield.yaml` |
| `--version`, `-v` | Print the version of `jf`. `jf version` prints the same string |
| `--help`, `-h` | Print the overview |

`jf` with no arguments prints the overview.

Every flag takes the GNU form. `--profile aws-smarta-staging` and
`--profile=aws-smarta-staging` both work.

## Workspace commands

These commands run a command line tool under the identity that the current
directory allows. They need a manifest. They do not need the hub.

### `jf run`

Run a command line tool under the identity of this directory.

```
jf run [--profile NAME] COMMAND [ARGS...]
```

`jf run` starts a CLI under one fixed identity. `jf` reads the working
directory, finds the workspace that owns it in `jackfield.yaml`, and picks the
profile that this workspace allows for that command. `jf` removes the ambient
credential variables and rejects the arguments that would select another
account, so the child process cannot change identity.

| Flag | What it does |
| --- | --- |
| `--profile NAME` | Select one of the profiles that this workspace allows |

```sh
jf run gog whoami --plain                                       # the account of this workspace
jf run wrangler r2 bucket list                                  # this workspace's Cloudflare profile
jf run --profile aws-smarta-staging aws sts get-caller-identity # pick one of several profiles
```

Every argument after the tool name belongs to that tool. `jf run gog --help`
prints the help of `gog`, not the help of `jf`, so a flag that both tools share
is never taken by the wrong one.

A workspace with more than one allowed profile and no default needs
`--profile`. AWS has no default on purpose, because staging and production
carry different risk.

`jf run` exits with the status of the child process, so a script reads the
tool's own result.

With the shims on `PATH`, the same commands are shorter:

```sh
gog whoami --plain
wrangler r2 bucket list
aws --jf-profile aws-smarta-staging sts get-caller-identity
```

### `jf resolve`

Show which identity a command would get, and run nothing.

```
jf resolve [--profile NAME] COMMAND [ARGS...]
```

`jf resolve` answers the question "which identity does this command get here?".
It does the same lookup as `jf run` and prints the workspace, the command, the
profile, and the executable. It starts no child process, so it is safe to run at
any time.

| Flag | What it does |
| --- | --- |
| `--profile NAME` | Select one of the profiles that this workspace allows |

```sh
jf resolve gog                              # which Google account gog gets here
jf resolve --profile aws-smarta-staging aws # which executable and profile an AWS command gets
```

The output names each part of the decision:

```
workspace=side-projects command=gog profile=gog-personal executable=/opt/homebrew/bin/gog
```

## Hub commands

These commands talk to the jackfield hub, which holds the credentials
themselves. The hub is the authority. This machine is a cache.

They need a hub address and, except for `jf login`, a device token. They do not
need a manifest, so a fresh machine can run `jf login` before any
`jackfield.yaml` exists there.

### `jf login`

Sign this machine in to the hub.

```
jf login [--name NAME] [--device-code | --browser]
```

`jf login` signs this machine in to the hub and stores a device token in
`~/.config/jackfield/device-token`. It prints a short code and a URL, then waits
while you approve the machine in a browser. Run it once per machine, and again
after you revoke this machine.

| Flag | What it does |
| --- | --- |
| `--name NAME` | The name this machine gets in `jf device list` (default: the short hostname) |
| `--device-code` | Print the code and URL for another device instead of opening a browser |
| `--browser` | Open a browser even when this machine looks headless |

```sh
jf login                # open a browser here
jf login --name macbook # name this machine in the device list
jf login --device-code  # over SSH: type the code on another device
```

`jf` picks the flow when you give no flag. A machine reached over SSH, or a
Linux machine with no graphical session, gets the device-code flow. That is a
guess about the environment, so `--device-code` and `--browser` override it.

Both flows use the same device grant (RFC 8628). The difference is only whether
this machine opens the browser itself. `jf login` prints the short code and the
URL in both flows, so a browser that fails to open costs one copy and paste.

A spinner turns while `jf` waits for the approval. On a pipe or in a script the
spinner prints its line once instead, so a log file gets no animation.

A second `jf login` on a machine that already holds a token revokes the old one
at the hub. The token file is replaced, so nothing on this machine could name the
old device afterwards, and it would stay in `jf device list` forever. The old
token revokes itself while it still works.

That revoke never fails the sign-in. When the hub refuses it, `jf` says so on
standard error and names `jf device revoke NAME`, and the new token is already
saved and working.

### `jf logout`

Sign this machine out of the hub.

```
jf logout
```

`jf logout` deletes this machine's device token and revokes it at the hub. It
revokes first, while the token still works, then deletes the local file.

`jf` deletes the local file even when the hub call fails, because the token on
this disk is what a person who takes this machine would read. `jf` then says the
hub call did not succeed, so you know to run `jf device revoke NAME` from
another machine.

A machine that holds no token is already signed out. `jf` says so and succeeds.

```sh
jf logout
```

Run `jf login` to sign in again.

### `jf status`

Show where every connection stands.

```
jf status
```

`jf status` prints one panel for this machine. It shows the hub address, the name
of this machine, and one line per connection: its identity, the age of its
credential, and whether the upstream service still accepts it. Run it first when
a tool fails and you do not know which credential is at fault.

```
hub     https://your-hub.workers.dev
device  macbook

CONNECTION          IDENTITY              AGE  UPSTREAM
slack-smarta        shreyans@example.com  2m   working
google-personal     you@example.com       2h   FAILING
cloudflare          unknown               5m   not probed yet
aws-smarta-staging  deploy@smarta         4d   working
```

On a terminal the `UPSTREAM` column carries colour: green for `working`, red for
`FAILING`, and grey for `not probed yet`. Each state also has its own word, so
the panel reads correctly with the colour off.

The hub does not probe the upstream services yet, so the column reads `not
probed yet` for every connection. That is the honest answer: nobody checked. A
credential shown there can still be one that Slack or Google already refused.

### `jf device`

List the machines that hold a device token, or revoke one.

```
jf device list
jf device revoke NAME
```

`jf device list` shows every machine that is signed in to the hub, and marks the
one you are on. `jf device revoke` removes one machine's token, by the name that
the list shows or by its device id. Any machine can revoke any other, so you can
revoke a lost laptop from the machine still in your hand.

```sh
jf device list                     # list every signed-in machine
jf device revoke grumpyorange      # revoke one machine
```

```
NAME          DEVICE ID   CREATED  LAST USED  
macbook       dev_aaa111  3d ago   30m ago    this machine
grumpyorange  dev_bbb222  1h ago   never
```

Revoking this machine is allowed. `jf` says so when it happens, because the next
hub command here then needs `jf login` again. To sign this machine out, prefer
[`jf logout`](#jf-logout): it also deletes the local token file.

Two machines with the same name are an error, not a guess. `jf` prints both
device ids and asks you to revoke by id, because revoking the wrong machine is
not something you can undo from the machine you just locked yourself out of.

### `jf cred`

Read one credential from the hub, or write one.

```
jf cred get [--no-cache] NAME
jf cred set [--identity WHO] [--stdin] [--ticket TICKET] NAME
```

Reading is cheap because an agent reads constantly. Writing is rare, and you are
present when it happens, so a write costs one browser approval.

#### `jf cred get`

Print one credential to standard output, and nothing else, so a script reads it
with a command substitution. `jf` caches the value under `~/.cache/jackfield`
for five minutes, and asks the hub again after that. This is mostly internal
plumbing, exposed for scripts and for a person who debugs a connection.

| Flag | What it does |
| --- | --- |
| `--no-cache` | Ask the hub even when a fresh cached copy exists |

```sh
token=$(jf cred get slack-smarta)
jf cred get --no-cache slack-smarta
```

Every message other than the secret goes to standard error, so a command
substitution captures the secret alone.

The five-minute cache means a credential that `jf cred set` replaced reaches
every machine within five minutes, with no action on those machines. It is also
long enough that a shell loop does not open a connection for every call.

#### `jf cred set`

Write one credential to the hub, where every machine then reads it. A write
needs a fresh browser approval every time, so `jf` opens the hub's approval page
and asks you to paste back the ticket it shows. `jf` reads the secret from a
hidden prompt, or from standard input with `--stdin`, and never from a command
argument.

| Flag | What it does |
| --- | --- |
| `--identity WHO` | Who this credential acts as, for the status panel |
| `--ticket TICKET` | An approval ticket from the hub's approval page |
| `--stdin` | Read the secret from standard input instead of prompting |

```sh
jf cred set slack-smarta                            # prompt for the secret
jf cred set --identity you@example.com slack-smarta # record who it acts as
printf '%s' "$SECRET" | jf cred set --stdin --ticket TICKET slack-smarta
```

A secret is never a command argument, because arguments appear in the process
list where any other process on the machine reads them.

The ticket works once, for that one connection, for five minutes. The secret
never passes through the browser: only the ticket does, and the secret goes
straight from this machine to the hub.

`jf cred set` clears this machine's cached copy after a write, so the next read
here fetches the value you just stored.

## Other commands

### `jf schema`

Print the whole command tree as JSON, for an agent.

```
jf schema --json
```

`jf schema --json` prints every command, its description, its flags, and its
positional arguments. The document is generated from the command tree itself, so
it cannot describe a command that `jf` does not have, and it cannot miss a
command that `jf` does have.

An agent that reads `jf --help` and `jf schema --json` can operate `jf` with no
other document.

```sh
jf schema --json                                  # the whole tree
jf schema --json | jq -r '.. | .path? // empty'   # every command path
```

The document has this shape:

```json
{
  "tool": "jf",
  "version": "v0.1.1",
  "description": "jf answers two questions...",
  "commands": [
    {
      "name": "cred",
      "path": "jf cred",
      "summary": "Read one credential from the hub, or write one",
      "description": "Work with the credentials that the hub holds...",
      "usage": "jf cred [flags]",
      "commands": [
        {
          "name": "set",
          "path": "jf cred set",
          "summary": "Store a credential in the hub",
          "usage": "jf cred set NAME [flags]",
          "arguments": [{ "name": "NAME", "variadic": false }],
          "flags": [
            {
              "name": "identity",
              "type": "string",
              "usage": "Who this credential acts as, for the status panel"
            }
          ],
          "inherited_flags": [{ "name": "config", "type": "string", "usage": "..." }],
          "examples": "  # Store a Slack credential..."
        }
      ]
    }
  ]
}
```

The hidden aliases and the shell completion commands are left out. The schema
teaches the current names only.

### `jf man`

Print the manual page, in the roff format that `man` reads.

```
jf man
```

The page is generated from the command tree, so it always describes the binary
that printed it. Both installers put a copy in
`~/.local/share/man/man1/jf.1`, so `man jf` works after an install.

```sh
jf man | man -l -                              # read the page now
jf man > ~/.local/share/man/man1/jf.1          # install it for this user
```

Inside a checkout, `make man` writes `docs/man/jf.1` from this command. Run it
after any change to a command name, a flag, or a help text, and commit the
result.

### `jf completion`

Print the shell completion script.

```
jf completion bash|zsh|fish|powershell
```

Run `jf completion SHELL --help` to see where your shell wants the file. For
zsh:

```sh
jf completion zsh > "${fpath[1]}/_jf"
```

### `jf version`

```
jf version
jf --version
jf -v
```

Print the version of `jf`. All three forms print the same string. A release build
prints its tag. A build from source prints the version that Go recorded, or
`dev`.

### `jf help`

```
jf --help
jf help COMMAND
jf COMMAND --help
```

`jf --help` prints the overview of every command. `jf help COMMAND` prints what
one command does, its flags, and its examples. `jf COMMAND --help` prints the
same page.

## Output and colour

`jf` paints its output only when the output is a terminal. A redirect to a file,
a pipe into `grep`, and a run inside a script all get plain text, so the output
stays parseable. The columns line up the same way in both forms.

| Variable | What it does |
| --- | --- |
| `NO_COLOR` | Any value turns the colour off. It wins over every other setting. |
| `CLICOLOR_FORCE` | Any value other than `0` turns the colour on, even for a pipe. |
| `TERM=dumb` | Turns the colour off, because that terminal shows escape codes as text. |

Colour is never the only signal. Each state carries its own word as well, so a
person who reads the plain output loses nothing.

## Files

| Path | What it holds |
| --- | --- |
| `jackfield.yaml` | The manifest: workspaces, the commands each allows, and the profiles each command may use. It also holds the `hub:` key. |
| `~/.config/jackfield/device-token` | This machine's device token, mode 0600 in a directory with mode 0700. |
| `~/.cache/jackfield/` | The cached credentials, mode 0600 per file. An entry lives for five minutes. |
| `~/.local/share/man/man1/jf.1` | The manual page, which `man jf` reads. |
| `~/.local/jackfield/bin/` | The optional `gog`, `wrangler`, and `aws` shims. |

`jf` searches for the manifest in the working directory, then in each parent
directory, then in `~/.config/jackfield/jackfield.yaml`.

`jf` refuses to read a token file that other users can read, because a leaked
device token reads every credential in the hub. The token is a file, not a
keychain item, because a headless machine has no unlocked login session and a
keychain item is unreadable there.

## Environment variables

| Variable | What it does |
| --- | --- |
| `JF_CONFIG` | The manifest to read. `--config` overrides it. |
| `JF_HUB` | The hub address. It overrides the `hub:` key of the manifest. |
| `JF_TOKEN_FILE` | The device token file, instead of the default path. |
| `JF_CACHE_DIR` | The credential cache directory, instead of the default path. |

`JF_HUB` overrides the manifest for one shell. Use it to reach a second
deployment without editing the file that every machine shares.

## When a command fails

Every message says what to do next. These are the ones you meet most.

| Message | What to do |
| --- | --- |
| `this machine has no device token; run jf login` | Run `jf login`. No hub command works until this machine is signed in. |
| `no hub address` | Add a `hub:` key to `jackfield.yaml` for every shell, or set `JF_HUB` for this one. |
| `found no jackfield.yaml here or in any parent directory` | Write one, or name a file with `--config PATH` or `JF_CONFIG`. |
| `the working directory ... is in no workspace of this manifest` | Add a workspace whose `roots:` cover this directory, or change directory. |
| `the workspace ... does not allow the command ...` | Add the command under that workspace's `commands:`, or run it elsewhere. |
| `this command has more than one allowed profile and no default` | Name one with `--profile`. The message lists the allowed profiles. |
| `... has mode 0644; other users can read it` | Run `chmod 600` on the token file, then `jf login` again. |
| `unknown command ...` | Run `jf --help` to see every command. |

## Renamed commands

The command tree was renamed. The old spellings appear in documents written
before the change.

| Old | New |
| --- | --- |
| `jf devices` | `jf device list` |
| `jf devices revoke NAME` | `jf device revoke NAME` |
| `jf creds get NAME` | `jf cred get NAME` |
| `jf auth NAME` | `jf cred set NAME` |

`jf auth` still runs, as a hidden alias of `jf cred set`. It prints a line that
names the new command, and it will be removed in a later release. Every other
old spelling is gone.

The flags moved to the GNU form at the same time. `-name` is now `--name`,
`-stdin` is now `--stdin`, and so on for every flag.

## See also

- [`docs/cli-gate.md`](cli-gate.md) — the gate and the hub in detail, including
  the manifest format, the interactive command rules, and the two installs.
- [`docs/design.md`](design.md) — why jackfield is a hub.
- `man jf` — the manual page, installed by both installers.
- `jf schema --json` — the same tree, for an agent.
