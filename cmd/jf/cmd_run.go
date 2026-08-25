package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shreyansqt/jackfield/internal/runner"
)

/* ------------------------------------------------------------------ */
/* jf run                                                              */
/* ------------------------------------------------------------------ */

func newRunCommand(environment *hubEnvironment, manifest func() (string, error)) *cobra.Command {
	var profile string

	command := &cobra.Command{
		Use:   "run COMMAND [ARGS...]",
		Short: "Run a command line tool under the identity of this directory",
		Long: `Start a command line tool under one fixed identity.

jf reads the working directory, finds the workspace that owns it in
jackfield.yaml, and picks the profile that this workspace allows for that
command. jf removes the ambient credential variables and rejects the arguments
that would select another account, so the child process cannot change identity.

A workspace with more than one allowed profile and no default needs --profile.
AWS has no default on purpose, because staging and production carry different
risk.

jf run exits with the status of the child process, so a script reads the tool's
own result.

The installed shims make the common commands shorter. With the shims on PATH,
"gog whoami --plain" and "aws --jf-profile aws-smarta-staging sts
get-caller-identity" run through jf as well.`,
		Example: `  # Run gog as the account of this workspace.
  jf run gog whoami --plain

  # Run wrangler under the Cloudflare profile of this workspace.
  jf run wrangler r2 bucket list

  # Pick the profile when the workspace allows more than one.
  jf run --profile aws-smarta-staging aws sts get-caller-identity`,
		Args: cobra.MinimumNArgs(1),
		// Every argument after the command name belongs to the child tool, so
		// cobra must not read them as flags of jf. Without this, `jf run gog
		// --help` would print the help of jf rather than the help of gog.
		DisableFlagsInUseLine: true,
		FParseErrWhitelist:    cobra.FParseErrWhitelist{UnknownFlags: false},
		RunE: func(command *cobra.Command, args []string) error {
			resolution, childArgs, err := resolveCommand(manifest, args[0], profile, args[1:])
			if err != nil {
				return err
			}
			return resolution.Exec(childArgs, os.Environ(), environment.Stdin, environment.Stdout, environment.Stderr)
		},
	}

	command.Flags().StringVar(&profile, "profile", "",
		"Select one of the profiles that this workspace allows")
	// Everything after the command name goes to the child tool untouched.
	command.Flags().SetInterspersed(false)
	return command
}

/* ------------------------------------------------------------------ */
/* jf resolve                                                          */
/* ------------------------------------------------------------------ */

func newResolveCommand(environment *hubEnvironment, manifest func() (string, error)) *cobra.Command {
	var profile string

	command := &cobra.Command{
		Use:   "resolve COMMAND [ARGS...]",
		Short: "Show which identity a command would get, and run nothing",
		Long: `Answer the question "which identity does this command get here?".

jf does the same lookup as "jf run" and prints the workspace, the command, the
profile, and the executable. It starts no child process, so it is safe to run at
any time.

The output names each part of the decision, in one line of key=value pairs:

  workspace=side-projects command=gog profile=gog-personal executable=/opt/homebrew/bin/gog`,
		Example: `  # See which Google account gog gets in this directory.
  jf resolve gog

  # See which executable and profile an AWS command gets.
  jf resolve --profile aws-smarta-staging aws`,
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, args []string) error {
			resolution, _, err := resolveCommand(manifest, args[0], profile, args[1:])
			if err != nil {
				return err
			}
			fmt.Fprintf(environment.Stdout, "workspace=%s command=%s profile=%s executable=%s\n",
				resolution.Workspace, resolution.Command, resolution.Profile, resolution.Launch.Executable)
			return nil
		},
	}

	command.Flags().StringVar(&profile, "profile", "",
		"Select one of the profiles that this workspace allows")
	command.Flags().SetInterspersed(false)
	return command
}

// resolveCommand does the manifest lookup that `jf run` and `jf resolve` share.
func resolveCommand(manifest func() (string, error), commandName string, profile string, args []string) (runner.Resolution, []string, error) {
	manifestPath, err := manifest()
	if err != nil {
		return runner.Resolution{}, nil, err
	}
	config, err := runner.Load(manifestPath)
	if err != nil {
		return runner.Resolution{}, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return runner.Resolution{}, nil, fmt.Errorf("read working directory: %w", err)
	}
	return config.ResolveArgs(cwd, commandName, profile, args)
}
