# CLI workspace gate

`jf run` selects a CLI identity from the current directory. The pilot manifest is
`jackfield.yaml`.

Build the CLI:

```sh
go build -o jf ./cmd/jf
```

Use `JF_CONFIG` when the current directory is outside this repository:

```sh
export JF_CONFIG=/Users/shreyans/workspaces/side-projects/jackfield/jackfield.yaml
jf resolve gog
jf run gog whoami --plain
jf run wrangler r2 bucket list
jf run --profile aws-smarta-staging aws sts get-caller-identity
```

The current profiles enforce these choices:

- Smarta uses the Smarta Google account and Wrangler's `default` profile.
- Side projects use the personal Google account and Wrangler's `personal` profile.
- Smarta AWS requires an explicit staging or production profile.
- AWS is not available from the side-projects workspace.

The launcher removes known ambient credential variables. It also rejects flags such as
`--profile`, `--account`, and `--access-token` after it selects an identity.

This design prevents accidental identity changes. It does not stop a process from
running the original CLI directly. A later trusted launcher can restrict `PATH`, issue
a short-lived workspace grant, and require that grant at each gate.
