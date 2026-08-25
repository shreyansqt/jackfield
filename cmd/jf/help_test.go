package main

import (
	"bytes"
	"strings"
	"testing"
)

// dispatchedCommands lists every command that main.go actually dispatches.
//
// This list is the other half of the guard below. A new command must appear
// here and in the help registry, so a person cannot add a command that the help
// output never mentions.
var dispatchedCommands = []string{
	"run", "resolve",
	"login", "status", "devices", "creds", "auth",
	"version", "help",
}

// TestEveryDispatchedCommandHasHelp is the guard the CLI needs most. A command
// that dispatches but has no help entry is a command nobody can discover.
func TestEveryDispatchedCommandHasHelp(t *testing.T) {
	for _, name := range dispatchedCommands {
		entry, found := commands[name]
		if !found {
			t.Errorf("the command %q dispatches but has no help entry in commands", name)
			continue
		}
		if entry.Name != name {
			t.Errorf("the help entry under key %q has Name %q", name, entry.Name)
		}
		if strings.TrimSpace(entry.Summary) == "" {
			t.Errorf("the command %q has no summary", name)
		}
		if strings.TrimSpace(entry.Description) == "" {
			t.Errorf("the command %q has no description", name)
		}
		if len(entry.Usage) == 0 {
			t.Errorf("the command %q has no usage line", name)
		}
		if len(entry.Examples) == 0 {
			t.Errorf("the command %q has no example", name)
		}
	}

	for name := range commands {
		found := false
		for _, dispatched := range dispatchedCommands {
			if dispatched == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the help entry %q describes a command that main.go does not dispatch", name)
		}
	}
}

// TestOverviewListsEveryCommand checks that the overview prints every command,
// not only the ones that a group happens to name.
func TestOverviewListsEveryCommand(t *testing.T) {
	var out bytes.Buffer
	printOverview(&out)
	text := out.String()

	for name := range commands {
		if !strings.Contains(text, name) {
			t.Errorf("the overview does not list the command %q", name)
		}
	}
	for name, entry := range commands {
		if !strings.Contains(text, entry.Summary) {
			t.Errorf("the overview does not print the summary of %q", name)
		}
	}
	if !strings.Contains(text, "jf help COMMAND") {
		t.Error("the overview does not point at `jf help COMMAND`")
	}
}

// TestEveryCommandIsInAGroup checks that a new command reaches the overview.
// A command missing from every group would not print, even with a help entry.
func TestEveryCommandIsInAGroup(t *testing.T) {
	grouped := map[string]bool{}
	for _, group := range commandGroups {
		for _, name := range group.Names {
			if _, found := commands[name]; !found {
				t.Errorf("the group %q names %q, which has no help entry", group.Title, name)
			}
			if grouped[name] {
				t.Errorf("the command %q appears in more than one group", name)
			}
			grouped[name] = true
		}
	}
	for name := range commands {
		if !grouped[name] {
			t.Errorf("the command %q is in no group, so the overview never prints it", name)
		}
	}
}

// TestCommandHelpPrintsTheParts checks the shape of one command page.
func TestCommandHelpPrintsTheParts(t *testing.T) {
	var out bytes.Buffer
	printCommandHelp(&out, commands["login"])
	text := out.String()

	for _, want := range []string{"USAGE", "FLAGS", "EXAMPLES", "jf login -name macbook", "-device-code"} {
		if !strings.Contains(text, want) {
			t.Errorf("the login help does not contain %q", want)
		}
	}
	// The description and the notes are prose, so their backticks are stripped.
	// The closing hint keeps its backticks, because they mark a literal command
	// that a person types.
	if strings.Contains(text, "`jf login` signs") {
		t.Error("the description still carries the backticks from the source text")
	}
	if !strings.Contains(text, "jf login signs this machine in") {
		t.Error("the description did not print as plain prose")
	}
}

// TestCommandHelpRendersEveryCommand checks that no command page panics or
// comes out empty.
func TestCommandHelpRendersEveryCommand(t *testing.T) {
	for name, entry := range commands {
		var out bytes.Buffer
		printCommandHelp(&out, entry)
		if out.Len() == 0 {
			t.Errorf("the help for %q is empty", name)
		}
		if !strings.Contains(out.String(), "jf "+name) {
			t.Errorf("the help for %q does not name the command", name)
		}
	}
}

func TestIsHelpRequest(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"-help"}} {
		if !isHelpRequest(args) {
			t.Errorf("%v should ask for the overview", args)
		}
	}
	for _, args := range [][]string{{"help", "login"}, {"status"}, {}} {
		if isHelpRequest(args) {
			t.Errorf("%v should not ask for the overview", args)
		}
	}
}

func TestWantsCommandHelpStopsAtDoubleDash(t *testing.T) {
	if !wantsCommandHelp([]string{"-h"}) {
		t.Error("-h asks for the command help")
	}
	if !wantsCommandHelp([]string{"gog", "--help"}) {
		t.Error("--help asks for the command help")
	}
	// Everything after `--` belongs to the child command, so `jf run -- gog -h`
	// asks gog for its help, not jf.
	if wantsCommandHelp([]string{"--", "gog", "-h"}) {
		t.Error("an -h after -- belongs to the child command")
	}
}

func TestUnknownCommandErrorSuggestsTheClosestName(t *testing.T) {
	err := unknownCommandError("devcies")
	if !strings.Contains(err.Error(), "jf devices") {
		t.Errorf("a near miss should suggest the command, got %v", err)
	}
	if !strings.Contains(err.Error(), "jf help") {
		t.Errorf("every unknown command should point at jf help, got %v", err)
	}

	// A word that resembles nothing gets the command list, not a wrong guess.
	far := unknownCommandError("photosynthesis")
	if strings.Contains(far.Error(), "Did you mean") {
		t.Errorf("a distant word should not get a suggestion, got %v", far)
	}
}

func TestRunHelpAnswersBothForms(t *testing.T) {
	var overview bytes.Buffer
	if err := runHelp(&overview, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overview.String(), "WORKSPACE COMMANDS") {
		t.Error("`jf help` should print the overview")
	}

	var page bytes.Buffer
	if err := runHelp(&page, []string{"creds"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.String(), "jf creds get") {
		t.Error("`jf help creds` should print the creds page")
	}

	if err := runHelp(&bytes.Buffer{}, []string{"nonsense"}); err == nil {
		t.Error("`jf help nonsense` should fail")
	}
}

// TestHubActionsMatchTheHelpRegistry checks that the hub dispatch table and the
// help registry describe the same set of hub commands.
func TestHubActionsMatchTheHelpRegistry(t *testing.T) {
	for _, name := range []string{"login", "status", "devices", "creds", "auth"} {
		if !isHubAction(name) {
			t.Errorf("%q should be a hub action", name)
		}
		if _, found := commands[name]; !found {
			t.Errorf("the hub action %q has no help entry", name)
		}
	}
	for _, name := range []string{"run", "resolve", "version", "help"} {
		if isHubAction(name) {
			t.Errorf("%q should not be a hub action", name)
		}
	}
}
