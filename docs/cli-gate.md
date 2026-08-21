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

This design prevents accidental identity changes. A process can still use an absolute
path such as `/opt/homebrew/bin/aws` to bypass the shim. A later trusted launcher can
restrict `PATH`, issue a short-lived workspace grant, and require that grant at each
gate.
