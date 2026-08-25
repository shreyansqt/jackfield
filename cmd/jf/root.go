package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mango "github.com/muesli/mango-cobra"
	"github.com/muesli/roff"
	"github.com/spf13/cobra"
)

// tagline is the one-line description of the tool. It answers "what is this?"
// for a person who reads `jf --help` before any documentation.
const tagline = "jf is the patchbay for agent credentials: workspace-scoped CLI identities, plus the hub."

// longDescription is the overview that `jf --help` prints under the tagline.
//
// It is deliberately thorough. An agent that reads `jf --help` and
// `jf schema --json` must be able to operate the tool with no other document.
const longDescription = `jf answers two questions. Which identity does a command line tool get in this
directory? And where does that credential come from?

A manifest, jackfield.yaml, answers the first question. It maps a directory to a
workspace, and a workspace to the profiles that each command may use. "jf run"
starts a tool under one of those profiles. It removes the ambient credential
variables, and it rejects the arguments that would select another account, so the
child process cannot change identity.

The jackfield hub answers the second question. The hub holds the credentials
themselves, and this machine is a cache. "jf login" signs this machine in to the
hub. "jf cred get" reads one credential, and "jf cred set" writes one.

A workspace command needs a manifest and no hub. A hub command needs a hub
address and, except for "jf login", a device token. It needs no manifest, so a
fresh machine runs "jf login" before any jackfield.yaml exists there.`

// newRootCommand builds the whole command tree.
//
// The tree is built rather than declared as a package variable, so a test builds
// a fresh tree with its own output streams and its own environment. Nothing in
// the tree reads a global.
func newRootCommand(environment *hubEnvironment) *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:   "jf",
		Short: tagline,
		Long:  longDescription,
		// A bare `jf` prints the overview rather than an error. A person who
		// types the bare name wants to learn what the tool does.
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return command.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&configPath, "config", "",
		"Read this manifest instead of searching for jackfield.yaml")

	// manifest finds the manifest for a command that needs one. The closure
	// captures the flag, so every subcommand reads the same value.
	manifest := func() (string, error) { return findConfig(configPath) }
	// optionalManifest finds the manifest and drops the failure. A hub command
	// reads the manifest only for its hub: key, and runs without one.
	optionalManifest := func() string {
		path, _ := findConfig(configPath)
		return path
	}

	root.AddCommand(
		newStatusCommand(environment, optionalManifest),
		newLoginCommand(environment, optionalManifest),
		newLogoutCommand(environment, optionalManifest),
		newDeviceCommand(environment, optionalManifest),
		newCredCommand(environment, optionalManifest),
		newRunCommand(environment, manifest),
		newResolveCommand(environment, manifest),
		newSchemaCommand(),
		newManCommand(),
		newVersionCommand(environment),
		newAuthAliasCommand(environment, optionalManifest),
	)

	return root
}

// newVersionCommand prints the version.
//
// `jf --version` prints the same string, and fang supplies that flag. The word
// exists as well because a person types `jf version` out of habit, and because
// the older documents name it. A tool that answers one form and not the other
// makes the reader guess which one this tool takes.
func newVersionCommand(environment *hubEnvironment) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of jf",
		Long: `Print the version of this jf binary.

A release build prints its tag. A build from source prints the version that Go
recorded, or "dev" when Go recorded none.

"jf --version" prints the same string.`,
		Example: `  # Print the version.
  jf version`,
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(environment.Stdout, "jf version %s\n", versionString())
			return err
		},
	}
}

// newManCommand prints the manual page.
//
// fang can add this command on its own, but it adds it hidden and with one line
// of text. The page is a real part of the documentation here, and `make man`
// regenerates docs/man/jf.1 from it, so jf supplies the command itself and
// passes fang.WithoutManpage(). The generator underneath is the same one that
// fang uses.
func newManCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "man",
		Short: "Print the manual page in roff format",
		Long: `Print the manual page for jf, in the roff format that man reads.

The page is generated from this command tree, so it always describes the binary
that printed it. Both installers put a copy in ~/.local/share/man/man1/jf.1, and
"make man" writes docs/man/jf.1 from this command.`,
		Example: `  # Read the page now.
  jf man | man -l -

  # Write the page into this checkout.
  jf man > docs/man/jf.1`,
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(command *cobra.Command, args []string) error {
			// The generator starts a new paragraph at every newline of a help
			// text, so the source line breaks would each become a blank line in
			// the rendered page. Joining each paragraph first lets man wrap the
			// text to the reader's own width, which is what a manual page does.
			source := manSource(command.Root())
			page, err := mango.NewManPage(1, source)
			if err != nil {
				return fmt.Errorf("build the manual page: %w", err)
			}
			_, err = fmt.Fprint(command.OutOrStdout(), page.Build(roff.NewDocument()))
			return err
		},
	}
}

// manSource returns a copy of the tree whose help texts suit a manual page.
//
// The copy is deep enough to reach every command, and it changes only the text.
// The real tree keeps its own line breaks, because a terminal help page is
// written to one width and a manual page is not.
func manSource(command *cobra.Command) *cobra.Command {
	copied := *command
	copied.Long = joinParagraphs(command.Long)
	copied.Short = strings.TrimSuffix(command.Short, ".")

	copied.ResetCommands()
	for _, child := range command.Commands() {
		// The deprecated aliases stay out, and so do the two commands that
		// cobra supplies. The completion subtree alone is longer than every
		// jackfield command together, and it describes the shell rather than
		// this tool. `man` is ours, so it stays.
		if child.Hidden || child.Name() == "completion" || child.Name() == "help" {
			continue
		}
		copied.AddCommand(manSource(child))
	}
	return &copied
}

// joinParagraphs makes each paragraph one line.
//
// A blank line still separates two paragraphs. Every other newline becomes a
// space, so the sentence that a help text wrapped at 80 columns reaches the man
// generator whole.
func joinParagraphs(text string) string {
	paragraphs := strings.Split(text, "\n\n")
	for index, paragraph := range paragraphs {
		paragraphs[index] = strings.Join(strings.Fields(paragraph), " ")
	}
	return strings.Join(paragraphs, "\n\n")
}

// findConfig returns the manifest to read.
//
// The order is deliberate. An explicit --config wins, because a person who names
// a file means that file. JF_CONFIG comes next, for a shell that works on one
// manifest. Otherwise jf searches the working directory and each parent, then the
// per-user manifest, so a directory inside a checkout needs no flag at all.
func findConfig(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if fromEnv := os.Getenv("JF_CONFIG"); fromEnv != "" {
		return fromEnv, nil
	}

	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(directory, "jackfield.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, ".config", "jackfield", "jackfield.yaml")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("found no jackfield.yaml here or in any parent directory, and none in ~/.config/jackfield/. " +
		"Write one there, or name a file with --config PATH or the JF_CONFIG environment variable")
}
