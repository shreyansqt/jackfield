# `jf` command reference

`jf` is the patchbay for agent credentials: workspace-scoped CLI identities, plus
the hub.

`jf` answers two questions. Which identity does a command line tool get in this
directory? And where does that credential come from? A manifest,
`jackfield.yaml`, answers the first. The hub answers the second.

This page is the written form of the built-in help. Every command here also
answers `jf help COMMAND`, and the whole tool answers `man jf`.

- [Getting started](#getting-started)
- [Workspace commands](#workspace-commands) — [`run`](#jf-run),
  [`resolve`](#jf-resolve)
- [Hub commands](#hub-commands) — [`login`](#jf-login),
  [`devices`](#jf-devices), [`status`](#jf-status), [`auth`](#jf-auth),
  [`creds`](#jf-creds)
- [Other commands](#other-commands) — [`version`](#jf-version),
  [`help`](#jf-help)
- [Files](#files)
- [Environment variables](#environment-variables)
- [When a command fails](#when-a-command-fails)

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
| `--version` | Print the version of `jf` |
| `--help` | Print the overview |

`jf` with no arguments prints the overview.

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
jf login [-name NAME] [-device-code | -browser]
```

`jf login` signs this machine in to the hub and stores a device token in
`~/.config/jackfield/device-token`. It prints a short code and a URL, then waits
while you approve the machine in a browser. Run it once per machine, and again
after you revoke this machine.

| Flag | What it does |
| --- | --- |
| `-name NAME` | The name this machine gets in `jf devices` (default: the short hostname) |
| `-device-code` | Print the code and URL for another device instead of opening a browser |
| `-browser` | Open a browser even when this machine looks headless |

```sh
jf login              # open a browser here
jf login -name macbook # name this machine in the device list
jf login -device-code  # over SSH: type the code on another device
```

`jf` picks the flow when you give no flag. A machine reached over SSH, or a
Linux machine with no graphical session, gets the device-code flow. That is a
guess about the environment, so `-device-code` and `-browser` override it.

Both flows use the same device grant (RFC 8628). The difference is only whether
this machine opens the browser itself. `jf login` prints the short code and the
URL in both flows, so a browser that fails to open costs one copy and paste.

### `jf devices`

List the machines that hold a device token, or revoke one.

```
jf devices
jf devices revoke NAME
```

`jf devices` lists every machine that is signed in to the hub, and marks the one
you are on. `jf devices revoke` removes one machine's token, by the name that the
list shows or by its device id. Any machine can revoke any other, so you can
revoke a lost laptop from the machine still in your hand.

```sh
jf devices                       # list every signed-in machine
jf devices revoke grumpyorange   # revoke one machine
```

Revoking this machine is allowed. `jf` says so when it happens, because the next
hub command here then needs `jf login` again.

Two machines with the same name are an error, not a guess. `jf` prints both
device ids and asks you to revoke by id, because revoking the wrong machine is
not something you can undo from the machine you just locked yourself out of.

### `jf status`

Show where every connection stands.

```
jf status
```

`jf status` prints one panel for this machine. It shows the hub address, the name
of this machine, and one line per connection: its identity, the age of its
credential, and whether the upstream service still accepts it. Run it first when
a tool fails and you do not know which credential is at fault.

The hub does not probe the upstream services yet, so the last column reads `not
probed yet`. That is the honest answer: nobody checked. A credential shown there
can still be one that Slack or Google already refused.

### `jf auth`

Store a credential in the hub.

```
jf auth [-identity WHO] [-stdin] [-ticket TICKET] CONNECTION
```

`jf auth` writes one credential to the hub, where every machine then reads it. A
write needs a fresh browser approval every time, so `jf` opens the hub's approval
page and asks you to paste back the ticket it shows. `jf` reads the secret from a
hidden prompt, or from standard input with `-stdin`, and never from a command
argument.

| Flag | What it does |
| --- | --- |
| `-identity WHO` | Who this credential acts as, for the status panel |
| `-ticket TICKET` | An approval ticket from the hub's approval page |
| `-stdin` | Read the secret from standard input instead of prompting |

```sh
jf auth slack-smarta                            # prompt for the secret
jf auth -identity you@example.com slack-smarta  # record who it acts as
printf '%s' "$SECRET" | jf auth -stdin -ticket TICKET slack-smarta
```

Reading is cheap because an agent reads constantly. Writing is rare, and you are
present when it happens, so a write costs one browser approval.

A secret is never a command argument, because arguments appear in the process
list where any other process on the machine reads them.

The ticket works once, for that one connection, for five minutes. The secret
never passes through the browser: only the ticket does, and the secret goes
straight from this machine to the hub.

`jf auth` clears this machine's cached copy after a write, so the next read here
fetches the value you just stored.

### `jf creds`

Read one credential from the hub, for scripts.

```
jf creds get [-no-cache] CONNECTION
```

`jf creds get` prints one credential to standard output, and nothing else, so a
script reads it with a command substitution. `jf` caches the value under
`~/.cache/jackfield` for five minutes, and asks the hub again after that. This is
mostly internal plumbing, exposed for scripts and for a person who debugs a
connection.

| Flag | What it does |
| --- | --- |
| `-no-cache` | Ask the hub even when a fresh cached copy exists |

```sh
token=$(jf creds get slack-smarta)
jf creds get -no-cache slack-smarta
```

Every message other than the secret goes to standard error, so a command
substitution captures the secret alone.

The five-minute cache means a credential that `jf auth` replaced reaches every
machine within five minutes, with no action on those machines. It is also long
enough that a shell loop does not open a connection for every call.

## Other commands

### `jf version`

```
jf version
jf --version
```

Print the version of `jf`. A release build prints its tag. A build from source
prints `dev`.

### `jf help`

```
jf help
jf help COMMAND
jf COMMAND -h
```

`jf help` prints the overview of every command. `jf help COMMAND` prints what one
command does, its flags, and its examples. `jf COMMAND -h` prints the same page.

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
| `unknown command ...` | Run `jf help` to see every command. |

## See also

- [`docs/cli-gate.md`](cli-gate.md) — the gate and the hub in detail, including
  the manifest format, the interactive command rules, and the two installs.
- [`docs/design.md`](design.md) — why jackfield is a hub.
- `man jf` — the manual page, installed by both installers.
