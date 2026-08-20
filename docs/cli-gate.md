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

The current profiles enforce these choices:

- Smarta uses the Smarta Google account and Wrangler's `default` profile.
- Side projects use the personal Google account and Wrangler's `personal` profile.
- Smarta AWS requires an explicit staging or production profile.
- AWS is not available from the side-projects workspace.

The launcher removes known ambient credential variables. It also rejects flags such as
`--profile`, `--account`, and `--access-token` after it selects an identity.

This design prevents accidental identity changes. A process can still use an absolute
path such as `/opt/homebrew/bin/aws` to bypass the shim. A later trusted launcher can
restrict `PATH`, issue a short-lived workspace grant, and require that grant at each
gate.
