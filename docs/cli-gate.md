# CLI workspace gate

`jf run` selects a CLI identity from the current directory. The pilot manifest is
`jackfield.yaml`.

## Two installs, for two kinds of machine

Installation has two steps, and most machines need only the first.

**A plain machine installs `jf` alone.** One command, no clone and no Go toolchain:

```sh
curl -fsSL https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh | sh
```

This puts `jf` in `~/.local/bin`. It is all a machine needs to reach the hub with
`jf login`, `jf status`, and `jf cred get`.

**A machine that gates CLIs also installs the shims.** The shims make `gog`,
`wrangler`, and `aws` run through the gate, so those commands cannot select
another identity. Run the second script only on a machine that needs that:

```sh
scripts/install-cli-shims.sh
```

The installer links the manifest under `~/.config/jackfield`. It adds `jf`, `gog`,
`wrangler`, and `aws` links to `~/.local/jackfield/bin`. The dotfiles shell config
puts this directory first on `PATH`.

The shim installer finds `jf` in this order:

1. `JF_BINARY`, if it is set.
2. A Go toolchain plus this repository: it builds `jf` from source.
3. An installed `jf` on `PATH`, such as the one `install.sh` wrote.

The source build comes first, because a developer who edits `jf` expects the shims
to run the code just edited. A machine with no clone falls through to the installed
binary. `JF_FROM_PATH=1` skips the build inside a clone.

Both installers also put the manual page in `~/.local/share/man/man1/jf.1`, so
`man jf` works after either install. `jf man` prints the same page in roff format,
which lets a machine install or reinstall the page without an installer:

```sh
jf man > ~/.local/share/man/man1/jf.1
```

`jf` generates the page from its own command tree, so the page always describes the
binary that printed it. Inside a checkout, `make man` rewrites `docs/man/jf.1`. Run
`make man` after you change a command name, a flag, or a help text, and commit the
result.

Use Jackfield directly:

```sh
export JF_CONFIG=/Users/shreyans/workspaces/side-projects/jackfield/jackfield.yaml
jf resolve gog
jf run gog whoami --plain
jf run wrangler r2 bucket list
jf run --profile aws-smarta-staging aws sts get-caller-identity
```

The shims make the common commands shorter:

```sh
gog whoami --plain
wrangler r2 bucket list
aws --jf-profile aws-smarta-staging sts get-caller-identity
```

Commands can also translate an upstream profile name to a Jackfield profile. This
keeps commands in shared documentation portable:

```yaml
aws:
  profiles: [aws-smarta-staging, aws-smarta-production]
  aliases:
    smt-group-aws-staging: aws-smarta-staging
    smt-group-aws-production: aws-smarta-production
```

With this map, both upstream argument forms select the mapped Jackfield profile:

```sh
aws --profile smt-group-aws-staging sts get-caller-identity
aws --profile=smt-group-aws-production sts get-caller-identity
```

Jackfield removes the upstream profile argument before it starts the child process.
An unknown alias fails. Thus, the child process cannot select a different identity.

The current profiles enforce these choices:

- Smarta uses the Smarta Google account and Wrangler's `default` profile.
- Side projects use the personal Google account and Wrangler's `personal` profile.
- Smarta AWS requires an explicit staging or production profile.
- AWS is not available from the side-projects workspace.

AWS intentionally has no default. Staging and production have different risk levels.
A command without a profile must fail instead of silently selecting either account.

The launcher removes known ambient credential variables. It also rejects flags such as
`--profile`, `--account`, and `--access-token` after it selects an identity.

## Subcommand overrides

A profile prefix fits the commands a person runs all day. It does not fit every
command the tool has. Two kinds of command break under it:

- **A login must reach a terminal.** A profile adds `--no-input` so that a command
  never waits for a person. `gog auth add` needs the opposite: it must prompt and
  open a browser.
- **An account command rejects the identity flag.** `wrangler whoami` and
  `wrangler login` fail when they are given `--profile`, which the profile adds to
  every call.

Both failures had the same shape. The gate blocked the command that repairs the
credential the gate depends on, and the only way through was the absolute path that
the gate exists to prevent. A profile can now name the subcommands that need a
different prefix:

```yaml
gog-personal:
  executable: /opt/homebrew/bin/gog
  prefix_args: [-a, shreyansqt@gmail.com, --no-input]
  denied_args: [-a, --account, --access-token, --client, --home]
  subcommand_overrides:
    - subcommand: [auth, add]
      drop_prefix_args: [--no-input]
      identity: shreyansqt@gmail.com

wrangler-personal:
  executable: /Users/shreyans/.local/bin/mise
  prefix_args: [exec, node@23, --, wrangler, --profile, personal]
  denied_args: [--profile]
  subcommand_overrides:
    - subcommand: [whoami]
      drop_prefix_args: [--profile]
    - subcommand: [login]
      drop_prefix_args: [--profile]
    - subcommand: [logout]
      drop_prefix_args: [--profile]
    - subcommand: [auth]
      drop_prefix_args: [--profile]
```

Each rule has three parts:

- `subcommand` — the words that select the command. Flags before or between those
  words do not affect the match, so `gog --verbose auth add` matches `auth add`.
  A rule matches on a prefix of the words, so the one-word `[auth]` rule covers
  `wrangler auth status`, `wrangler auth create`, and `wrangler auth keyring`.
- `drop_prefix_args` — the prefix arguments to remove for this command. A dropped
  flag takes its value with it, so dropping `--profile` removes both `--profile`
  and `personal`. The manifest fails to load when a rule drops an argument that
  the profile's `prefix_args` does not contain, because such a rule silently does
  nothing.
- `identity` — the account this command may run for. It is optional. The wrangler
  rules omit it, because wrangler takes no account argument.

`subcommand_overrides` was called `interactive` when only the gog rule existed.
That key still parses, so a machine with an older manifest keeps working. New rules
should use `subcommand_overrides`.

### The identity check

The identity check reads the account from the positional arguments, because
`gog auth add` takes the email as an argument rather than a flag. Every word after
the subcommand must equal the pinned identity:

```sh
cd ~/workspaces/side-projects
gog auth add shreyansqt@gmail.com     # allowed; opens a browser
gog auth add someone@example.com      # denied
```

The denied-argument list still applies to every command, with or without a rule.
`gog auth add --account someone@example.com` fails, and so does
`wrangler whoami --profile other`. A rule drops the flag that Jackfield adds; it
never lets the child supply its own. Commands without a matching rule are unchanged,
including `gog auth list`, `gog auth remove`, and every wrangler resource command:

```sh
cd ~/workspaces/smarta
wrangler whoami            # runs without --profile
wrangler r2 bucket list    # runs with --profile default
```

Only `gog auth add` has a gog rule today. Two other commands were considered and
rejected:

- `gog auth manage` opens a browser manager for every stored account. The identity
  check reads command arguments, so it cannot limit what a person does in that page.
- `gog auth remove` does not need a browser, so it keeps `--no-input`.

### What the gate does not promise for wrangler login

Read this before you use `wrangler login` through the shim.

`wrangler login` and `wrangler logout` run without `--profile`. Wrangler therefore
writes whatever profile it considers active on this machine, and Jackfield has no
say in which one that is. A `wrangler login` from the smarta workspace can overwrite
the personal credential, and the reverse is equally true.

State it plainly: **`login` and `logout` manage the machine's wrangler
authentication store as a whole. The gate guarantees the identity of resource
commands only.** `wrangler r2 bucket list` in the smarta workspace always carries
`--profile default`. `wrangler login` in the same directory carries no such promise.

This is a limit in wrangler, not a gap left open here. Wrangler rejects `--profile`
on those commands, so there is no argument the gate could add to make the login
profile-safe. Do not work around it with an environment variable or a wrapper: the
next wrangler release would change the answer without warning. Check `wrangler
whoami` after a login, and know which account you signed in as.

`wrangler auth keyring` controls whether OAuth credentials reach the OS keychain.
That matters on a headless machine, where the macOS keychain needs an unlocked login
session. The `[auth]` rule makes that command reachable through the shim.

The identity check reads arguments that the caller supplies. It prevents an accidental
login to the wrong account. It is not a hard boundary. A process can still call
`/opt/homebrew/bin/gog` directly, as described below.

This design prevents accidental identity changes. A process can still use an absolute
path such as `/opt/homebrew/bin/aws` to bypass the shim. A later trusted launcher can
restrict `PATH`, issue a short-lived workspace grant, and require that grant at each
gate.

## Hub commands

The gate above selects an identity on this machine. The hub commands below reach
the jackfield hub, which holds the credentials themselves. The hub is the
authority. This machine is a cache.

### The hub address

`jf` finds the hub in this order:

1. The `JF_HUB` environment variable, if it is set.
2. A `hub:` key in `jackfield.yaml`.

The manifest is the normal place, because every machine that reads the same
manifest then reaches the same hub with no further setup:

```yaml
version: 1
hub: https://jackfield-hub.example.workers.dev

workspaces:
  ...
```

`JF_HUB` overrides the manifest for one shell. Use it to reach a second
deployment without editing the file every machine shares.

### Sign a machine in

```sh
jf login                  # opens a browser on this machine
jf login --device-code    # prints a code to type on another device
jf login --browser        # opens a browser even on a machine that looks headless
jf login --name mini      # names this machine in `jf device list`
```

Both flows use the same device grant (RFC 8628). The difference is only whether
this machine opens the browser itself. `jf login` prints the short code and the
URL in both flows, so a browser that fails to open costs one copy and paste.

`jf login` chooses the flow when you give no flag. A machine reached over SSH,
or a Linux machine with no graphical session, gets the device-code flow. This is
a guess about the environment, not a fact, so `--device-code` and `--browser`
override it.

The device name defaults to this machine's short hostname. It is what
`jf device list` shows, and you can change it in the browser before you approve.

### Where the token is stored

`jf login` writes the device token to:

```
~/.config/jackfield/device-token
```

The file has mode 0600 and its directory has mode 0700. `JF_TOKEN_FILE`
overrides the location.

This is a file, not a keychain item, on purpose. The Mac mini runs headless, and
the macOS keychain needs an unlocked login session, so a keychain item is
unreadable there. `jf` refuses to read a token file that other users can read,
because a leaked device token reads every credential in the hub.

### Sign this machine out

```sh
jf logout
```

`jf logout` revokes this machine's token at the hub first, while the token still
works. It then deletes the local file. It deletes the local file even when the
hub call fails, because the token on this disk is what a person who takes the
machine reads.

When the hub call fails, `jf` says so on standard error. The hub then still holds
the token, so revoke it from another machine with `jf device revoke NAME`.

A machine with no token is already signed out. `jf` says so and exits with status
0. Run `jf login` to sign this machine in again.

### See where every connection stands

```sh
jf status
```

The panel shows the hub address, this machine's device name, and one line for
each connection: its identity, the age of its credential, and whether the
upstream service still accepts it.

The hub does not probe the upstream services yet. Until it does, the last column
reads `not probed yet` rather than a tick. That is the honest answer: nobody
checked. A credential shown there can still be one that Slack or Google already
refused.

### List and revoke machines

```sh
jf device list
jf device revoke grumpyorange
```

`jf device list` lists every machine that holds a device token, and marks the one
you are on. `jf device revoke` takes the machine name that list shows, and it also
accepts a device id.

Any machine can revoke any other, including itself. That is deliberate: you
revoke a lost laptop from the machine still in your hand. Revoking this machine
is allowed, and `jf` says so, because the next command here then needs
`jf login` again.

Two machines with the same name are an error rather than a guess. `jf` prints
both device ids and asks you to revoke by id, because revoking the wrong machine
is not something you can undo from the machine you just locked yourself out of.

### Read a credential

```sh
jf cred get slack-smarta
jf cred get --no-cache slack-smarta
```

The secret goes to standard output alone, so a script reads it with a command
substitution. Every other message goes to standard error.

This is mostly internal plumbing. It is exposed for scripting, and because a
person debugging a connection needs it.

The value is cached under `~/.cache/jackfield/`, with mode 0600 on each file and
mode 0700 on the directory. An entry lives for five minutes. That is short
enough that a credential `jf cred set` replaced reaches every machine within five
minutes, with no action on those machines, and long enough that a shell loop
does not open a connection for every call. `--no-cache` asks the hub anyway.
`JF_CACHE_DIR` overrides the location.

The shims do not fetch credentials from the hub yet. `gog`, `wrangler`, and
`aws` still use the identity the gate selects and the credentials already on the
machine. That change lands after a real hub deployment proves this flow.

### Store a credential

```sh
jf cred set slack-smarta
jf cred set --identity you@example.com slack-smarta
printf '%s' "$SECRET" | jf cred set --stdin --ticket TICKET slack-smarta
```

Writing a credential needs a fresh browser approval, every time. A device token
is never enough. Reading is cheap because an agent reads constantly; writing is
rare and you are present when it happens.

The secret is read from a hidden prompt, or from standard input with `--stdin`.
It is never a command argument, because arguments appear in the process list
where any other process on the machine reads them.

`jf cred set` clears this machine's cached copy after a write, so the next read
here fetches the value you just stored.

`jf cred set` opens the hub's approval page at `/approvals?connection=<name>`. The
page names the connection you are about to write, you approve it, and it shows
a ticket. Paste that ticket back at the prompt. `--ticket` skips the prompt when
you already have one.

The ticket works once, for that one connection, for five minutes. The secret
never passes through the browser: only the ticket does, and the secret goes
straight from this machine to the hub.
