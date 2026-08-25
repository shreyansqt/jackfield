package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newTestTree builds the command tree with no machine behind it.
//
// The help tests read the tree's text, so they need no hub, no manifest, and no
// terminal.
func newTestTree() *cobra.Command {
	return newRootCommand(defaultHubEnvironment(""))
}

// walk calls visit for the root command and every command under it.
func walk(command *cobra.Command, visit func(*cobra.Command)) {
	visit(command)
	for _, child := range command.Commands() {
		walk(child, visit)
	}
}

// TestEveryCommandHasHelpText is the guard the CLI needs most. A command that
// dispatches but has no help is a command nobody can discover.
//
// This is the Cobra form of the guard that the old hand-written help registry
// carried. The tree is now the single source, so a new command cannot exist
// without the text that describes it.
func TestEveryCommandHasHelpText(t *testing.T) {
	walk(newTestTree(), func(command *cobra.Command) {
		// The framework supplies these, and their text is not ours to write.
		switch command.Name() {
		case "help", "completion", "man", "bash", "zsh", "fish", "powershell":
			return
		}
		path := command.CommandPath()

		if strings.TrimSpace(command.Short) == "" {
			t.Errorf("the command %q has no Short summary", path)
		}
		// A hidden alias needs a summary so `jf help auth` says something, but
		// it needs no full page: it exists only to point at the current name.
		if command.Hidden {
			return
		}
		if strings.TrimSpace(command.Long) == "" {
			t.Errorf("the command %q has no Long description", path)
		}
		// A command that only groups subcommands needs no example of its own,
		// because each subcommand under it carries one.
		if !command.HasSubCommands() && strings.TrimSpace(command.Example) == "" {
			t.Errorf("the command %q has no example", path)
		}
	})
}

// TestEveryFlagHasUsageText checks that no flag reaches the help as a bare name.
func TestEveryFlagHasUsageText(t *testing.T) {
	walk(newTestTree(), func(command *cobra.Command) {
		command.NonInheritedFlags().VisitAll(func(item *pflag.Flag) {
			if strings.TrimSpace(item.Usage) == "" {
				t.Errorf("the flag --%s of %q has no usage text", item.Name, command.CommandPath())
			}
		})
	})
}

// TestTheCommandTreeIsTheAgreedShape names every command the CLI promises.
//
// A rename is a breaking change for a person's muscle memory and for every
// document that names the old word, so it must be a deliberate edit here rather
// than a silent drift.
func TestTheCommandTreeIsTheAgreedShape(t *testing.T) {
	wanted := []string{
		"jf status",
		"jf login",
		"jf logout",
		"jf device",
		"jf device list",
		"jf device revoke",
		"jf cred",
		"jf cred get",
		"jf cred set",
		"jf run",
		"jf resolve",
		"jf schema",
	}

	found := map[string]bool{}
	walk(newTestTree(), func(command *cobra.Command) {
		found[command.CommandPath()] = true
	})
	for _, path := range wanted {
		if !found[path] {
			t.Errorf("the command tree has no %q", path)
		}
	}
}

// TestHelpNamesEveryCommand checks that `jf --help` lists the whole tree.
func TestHelpNamesEveryCommand(t *testing.T) {
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	for _, name := range []string{"status", "login", "logout", "device", "cred", "run", "resolve", "schema"} {
		if !strings.Contains(text, name) {
			t.Errorf("the overview does not list %q", name)
		}
	}
	// The alias is hidden, so it must not teach a new caller the old name.
	if strings.Contains(text, "\n  auth") {
		t.Error("the overview lists the deprecated `auth` alias")
	}
}

/* ------------------------------------------------------------------ */
/* jf schema --json                                                    */
/* ------------------------------------------------------------------ */

// TestSchemaDescribesTheWholeTree checks that the JSON an agent reads carries
// every command, with the flags and arguments that each one takes.
func TestSchemaDescribesTheWholeTree(t *testing.T) {
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"schema", "--json"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	var document schemaDocument
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatalf("the schema is not valid JSON: %v", err)
	}
	if document.Tool != "jf" {
		t.Errorf("got tool %q, want jf", document.Tool)
	}
	if strings.TrimSpace(document.Description) == "" {
		t.Error("the schema carries no description of the tool")
	}

	paths := map[string]schemaCommand{}
	var collect func([]schemaCommand)
	collect = func(commands []schemaCommand) {
		for _, command := range commands {
			paths[command.Path] = command
			collect(command.Commands)
		}
	}
	collect(document.Commands)

	for _, path := range []string{"jf status", "jf login", "jf logout", "jf device list", "jf device revoke", "jf cred get", "jf cred set", "jf run", "jf resolve"} {
		command, found := paths[path]
		if !found {
			t.Errorf("the schema has no %q", path)
			continue
		}
		if strings.TrimSpace(command.Summary) == "" {
			t.Errorf("the schema entry %q has no summary", path)
		}
	}

	// The positional arguments must reach the schema, or an agent cannot call
	// the command that needs one.
	revoke := paths["jf device revoke"]
	if len(revoke.Arguments) != 1 || revoke.Arguments[0].Name != "NAME" {
		t.Errorf("got arguments %v for `jf device revoke`, want one named NAME", revoke.Arguments)
	}

	// So must the flags.
	credSet := paths["jf cred set"]
	flagNames := map[string]bool{}
	for _, flag := range credSet.Flags {
		flagNames[flag.Name] = true
	}
	for _, name := range []string{"identity", "ticket", "stdin"} {
		if !flagNames[name] {
			t.Errorf("the schema entry for `jf cred set` has no --%s flag", name)
		}
	}

	// The deprecated alias is hidden, so a new caller never learns it here.
	if _, found := paths["jf auth"]; found {
		t.Error("the schema teaches the deprecated `auth` alias")
	}
}

// The schema is generated from the tree, so it cannot fall behind the tree. This
// test states that property directly: every visible command in the tree has a
// schema entry.
func TestSchemaCannotGoStale(t *testing.T) {
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"schema", "--json"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	var document schemaDocument
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	described := map[string]bool{}
	var collect func([]schemaCommand)
	collect = func(commands []schemaCommand) {
		for _, command := range commands {
			described[command.Path] = true
			collect(command.Commands)
		}
	}
	collect(document.Commands)

	walk(newTestTree(), func(command *cobra.Command) {
		if command.Hidden || command.Name() == "jf" {
			return
		}
		switch command.Name() {
		case "help", "completion":
			return
		}
		// A command under a hidden or framework parent is out of scope too.
		if parent := command.Parent(); parent != nil && (parent.Hidden || parent.Name() == "completion") {
			return
		}
		if !described[command.CommandPath()] {
			t.Errorf("the tree has %q but the schema does not describe it", command.CommandPath())
		}
	})
}

func TestSchemaRefusesWithoutTheJSONFlag(t *testing.T) {
	root := newTestTree()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"schema"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("`jf schema` without --json should fail and say what to run")
	}
}

/* ------------------------------------------------------------------ */
/* jf man                                                              */
/* ------------------------------------------------------------------ */

// The manual page is generated from the tree, so it must name every command.
func TestManPageDescribesEveryCommand(t *testing.T) {
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"man"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	page := out.String()
	if !strings.HasPrefix(page, ".TH JF 1") {
		t.Fatalf("the page does not start with a roff title header:\n%.80s", page)
	}
	for _, name := range []string{"status", "login", "logout", "device", "cred", "run", "resolve", "schema"} {
		if !strings.Contains(page, name) {
			t.Errorf("the manual page does not describe %q", name)
		}
	}
	// The shell completion subtree describes the shell, not jackfield, and it
	// would be longer than the rest of the page together.
	if strings.Contains(page, "autocompletion script for bash") {
		t.Error("the manual page carries the shell completion subtree")
	}
	// The deprecated alias must not teach a new reader the old name.
	if strings.Contains(page, "jf auth") {
		t.Error("the manual page teaches the deprecated `auth` alias")
	}
}

// A help text wraps at 80 columns for a terminal. A manual page must not keep
// those breaks, because man wraps to the reader's own width.
func TestJoinParagraphsKeepsParagraphsApart(t *testing.T) {
	joined := joinParagraphs("one line\nand its wrap\n\na second paragraph\nwrapped too")
	want := "one line and its wrap\n\na second paragraph wrapped too"
	if joined != want {
		t.Fatalf("got %q, want %q", joined, want)
	}
}

// TestTheCommittedManPageIsCurrent catches a stale docs/man/jf.1.
//
// The page is generated, and both installers ship the committed copy. So a page
// that falls behind the command tree is a page that describes commands the
// installed binary does not have. `make man` regenerates it.
//
// The test skips when it cannot find the file, so it does not fail a build that
// runs outside a checkout.
func TestTheCommittedManPageIsCurrent(t *testing.T) {
	const pagePath = "../../docs/man/jf.1"
	committed, err := os.ReadFile(pagePath)
	if err != nil {
		t.Skipf("no committed manual page to check: %v", err)
	}

	root := newTestTree()
	var generated bytes.Buffer
	root.SetOut(&generated)
	root.SetErr(&generated)
	root.SetArgs([]string{"man"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The page carries the date it was built, so a copy generated on a later day
	// differs by that one line and nothing else. Comparing the rest keeps the
	// test from failing every midnight.
	if withoutTitleLine(generated.String()) != withoutTitleLine(string(committed)) {
		t.Errorf("%s is behind the command tree. Run `make man` and commit the result.", pagePath)
	}
}

// withoutTitleLine drops the .TH line, which carries the build date.
func withoutTitleLine(page string) string {
	lines := strings.Split(page, "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, ".TH ") {
			return strings.Join(append(lines[:index:index], lines[index+1:]...), "\n")
		}
	}
	return page
}
