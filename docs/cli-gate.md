# CLI workspace gate

`jf run` selects a CLI identity from the current directory. The pilot manifest is
`jackfield.yaml`.

Install the CLI and the automatic command shims:

```sh
scripts/install-cli-shims.sh
```

The installer builds `jf` under `~/.local/lib/jackfield`. It links the manifest under
`~/.config/jackfield`. It adds `jf`, `gog`, `wrangler`, and `aws` links to
`~/.local/jackfield/bin`. The dotfiles shell config puts this directory first on
`PATH`.

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

## Interactive commands

A profile normally adds `--no-input` so that a command never waits for a person. A
login command needs the opposite. `gog auth add` must open a browser and wait. Before
this rule existed, a Google token could only be renewed with the absolute path
`/opt/homebrew/bin/gog`, which is the bypass the gate exists to prevent.

A profile can now mark some subcommands as interactive:

```yaml
gog-personal:
  executable: /opt/homebrew/bin/gog
  prefix_args: [-a, shreyansqt@gmail.com, --no-input]
  denied_args: [-a, --account, --access-token, --client, --home]
  interactive:
    - subcommand: [auth, add]
      drop_prefix_args: [--no-input]
      identity: shreyansqt@gmail.com
```

Each rule has three parts:

- `subcommand` — the words that select the command. Flags before or between those
  words do not affect the match, so `gog --verbose auth add` matches `auth add`.
- `drop_prefix_args` — the prefix arguments to remove for this command. Dropping
  `--no-input` lets the command prompt and open a browser.
- `identity` — the account this command may run for.

The identity check reads the account from the positional arguments, because
`gog auth add` takes the email as an argument rather than a flag. Every word after
the subcommand must equal the pinned identity:

```sh
cd ~/workspaces/side-projects
gog auth add shreyansqt@gmail.com     # allowed; opens a browser
gog auth add someone@example.com      # denied
```

The denied-argument list still applies. `gog auth add --account someone@example.com`
fails, the same as any other command. Commands without a matching rule are unchanged
and keep `--no-input`, including `gog auth list` and `gog auth remove`.

Only `gog auth add` has a rule today. Two other commands were considered and rejected:

- `gog auth manage` opens a browser manager for every stored account. The identity
  check reads command arguments, so it cannot limit what a person does in that page.
- `gog auth remove` does not need a browser, so it keeps `--no-input`.

The identity check reads arguments that the caller supplies. It prevents an accidental
login to the wrong account. It is not a hard boundary. A process can still call
`/opt/homebrew/bin/gog` directly, as described below.

This design prevents accidental identity changes. A process can still use an absolute
path such as `/opt/homebrew/bin/aws` to bypass the shim. A later trusted launcher can
restrict `PATH`, issue a short-lived workspace grant, and require that grant at each
gate.
