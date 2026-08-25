package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// tagline is the one-line description of the tool. It answers "what is this?"
// for a person who reads `jf help` before any documentation.
const tagline = "jf is the patchbay for agent credentials: workspace-scoped CLI identities, plus the hub."

// commandGroup names one section of the overview.
type commandGroup struct {
	// Title heads the section, for example "Workspace commands".
	Title string
	// Blurb says in one line what the commands in this section do.
	Blurb string
	// Names lists the commands of this section, in the order to print them.
	Names []string
}

// command describes one `jf` command for the help system.
//
// Every command has an entry here. The help text and the dispatch table read
// the same list, so a command cannot exist without its help. A test checks that.
type command struct {
	// Name is the word the person types, for example "login".
	Name string
	// Summary is the one line the overview prints. It starts with a verb and
	// has no full stop, because the overview reads as a list.
	Summary string
	// Usage lists the usage lines for this command, without the leading "jf".
	Usage []string
	// Description says what the command does, in 2 or 3 plain sentences.
	Description string
	// Examples pairs a command line with the reason to run it.
	Examples []example
	// Flags registers this command's flags on a fresh flag set, so the help
	// text and the real command describe the same flags. It may be nil.
	Flags func(*flag.FlagSet)
	// Notes holds extra paragraphs that print after the flags.
	Notes []string
}

// example is one command line, with the reason a person runs it.
type example struct {
	Command string
	Comment string
}

// commandGroups is the order of the overview. A new group needs a line here.
var commandGroups = []commandGroup{
	{
		Title: "Workspace commands",
		Blurb: "Run a CLI under the identity that the current directory allows.",
		Names: []string{"run", "resolve"},
	},
	{
		Title: "Hub commands",
		Blurb: "Talk to the jackfield hub, which holds the credentials themselves.",
		Names: []string{"login", "devices", "status", "auth", "creds"},
	},
	{
		Title: "Other commands",
		Blurb: "",
		Names: []string{"version", "help"},
	},
}

// commands describes every command. The map key is the command name.
var commands = map[string]*command{
	"run": {
		Name:    "run",
		Summary: "Run a CLI under the identity of this directory",
		Usage: []string{
			"run [--profile NAME] COMMAND [ARGS...]",
		},
		Description: "`jf run` starts a CLI under one fixed identity. " +
			"jf reads the working directory, finds the workspace that owns it in `jackfield.yaml`, and picks the profile that this workspace allows for that command. " +
			"jf removes the ambient credential variables and rejects the arguments that would select another account, so the child process cannot change identity.",
		Flags: func(flags *flag.FlagSet) {
			flags.String("profile", "", "Select one of the profiles that this workspace allows")
		},
		Examples: []example{
			{"jf run gog whoami --plain", "Run gog as the account of this workspace"},
			{"jf run wrangler r2 bucket list", "Run wrangler under the Cloudflare profile of this workspace"},
			{"jf run --profile aws-smarta-staging aws sts get-caller-identity", "Pick the profile when the workspace allows more than one"},
		},
		Notes: []string{
			"A workspace with more than one allowed profile and no default needs --profile. " +
				"AWS has no default on purpose, because staging and production carry different risk.",
			"The installed shims make the common commands shorter. " +
				"With the shims on PATH, `gog whoami --plain` and `aws --jf-profile aws-smarta-staging sts get-caller-identity` run through jf as well.",
		},
	},
	"resolve": {
		Name:    "resolve",
		Summary: "Show which identity a command would get, and run nothing",
		Usage: []string{
			"resolve [--profile NAME] COMMAND [ARGS...]",
		},
		Description: "`jf resolve` answers the question \"which identity does this command get here?\". " +
			"It does the same lookup as `jf run` and prints the workspace, the command, the profile, and the executable. " +
			"It starts no child process, so it is safe to run at any time.",
		Flags: func(flags *flag.FlagSet) {
			flags.String("profile", "", "Select one of the profiles that this workspace allows")
		},
		Examples: []example{
			{"jf resolve gog", "See which Google account gog gets in this directory"},
			{"jf resolve --profile aws-smarta-staging aws", "See which executable and profile an AWS command gets"},
		},
	},
	"login": {
		Name:    "login",
		Summary: "Sign this machine in to the hub",
		Usage: []string{
			"login [--name NAME] [--device-code | --browser]",
		},
		Description: "`jf login` signs this machine in to the hub and stores a device token in `~/.config/jackfield/device-token`. " +
			"It prints a short code and a URL, then waits while you approve the machine in a browser. " +
			"Run it once per machine, and again after you revoke this machine.",
		Flags: func(flags *flag.FlagSet) {
			flags.Bool("device-code", false, "Print the code and URL for another device instead of opening a browser")
			flags.Bool("browser", false, "Open a browser even when this machine looks headless")
			flags.String("name", "", "The name this machine gets in `jf devices` (default: the short hostname)")
		},
		Examples: []example{
			{"jf login", "Sign in, and let jf open a browser here"},
			{"jf login -name macbook", "Sign in and name this machine in the device list"},
			{"jf login -device-code", "Sign in over SSH, with a code you type on another device"},
		},
		Notes: []string{
			"jf picks the flow when you give no flag. " +
				"A machine reached over SSH, or a Linux machine with no graphical session, gets the device-code flow. " +
				"That is a guess about the environment, so --device-code and --browser override it.",
			"`jf login` needs the hub address. Set `hub:` in jackfield.yaml, or set the JF_HUB environment variable.",
		},
	},
	"devices": {
		Name:    "devices",
		Summary: "List the machines that hold a device token, or revoke one",
		Usage: []string{
			"devices",
			"devices revoke NAME",
		},
		Description: "`jf devices` lists every machine that is signed in to the hub, and marks the one you are on. " +
			"`jf devices revoke` removes one machine's token, by the name that the list shows or by its device id. " +
			"Any machine can revoke any other, so you can revoke a lost laptop from the machine still in your hand.",
		Examples: []example{
			{"jf devices", "List every signed-in machine"},
			{"jf devices revoke grumpyorange", "Revoke the machine named grumpyorange"},
		},
		Notes: []string{
			"Revoking this machine is allowed. jf says so when it happens, because the next hub command here then needs `jf login` again.",
			"Two machines with the same name are an error, not a guess. " +
				"jf prints both device ids and asks you to revoke by id.",
		},
	},
	"status": {
		Name:    "status",
		Summary: "Show where every connection stands",
		Usage: []string{
			"status",
		},
		Description: "`jf status` prints one panel for this machine. " +
			"It shows the hub address, the name of this machine, and one line per connection: its identity, the age of its credential, and whether the upstream service still accepts it. " +
			"Run it first when a tool fails and you do not know which credential is at fault.",
		Examples: []example{
			{"jf status", "See the hub, this machine, and every connection"},
		},
		Notes: []string{
			"The hub does not probe the upstream services yet, so the last column reads `not probed yet`. " +
				"That is the honest answer: nobody checked. A credential shown there can still be one that Slack or Google already refused.",
		},
	},
	"auth": {
		Name:    "auth",
		Summary: "Store a credential in the hub",
		Usage: []string{
			"auth [--identity WHO] [--stdin] [--ticket TICKET] CONNECTION",
		},
		Description: "`jf auth` writes one credential to the hub, where every machine then reads it. " +
			"A write needs a fresh browser approval every time, so jf opens the hub's approval page and asks you to paste back the ticket it shows. " +
			"jf reads the secret from a hidden prompt, or from standard input with --stdin, and never from a command argument.",
		Flags: func(flags *flag.FlagSet) {
			flags.String("identity", "", "Who this credential acts as, for the status panel")
			flags.String("ticket", "", "An approval ticket from the hub's approval page")
			flags.Bool("stdin", false, "Read the secret from standard input instead of prompting")
		},
		Examples: []example{
			{"jf auth slack-smarta", "Store a Slack credential, with a prompt for the secret"},
			{"jf auth -identity you@example.com slack-smarta", "Record who the credential acts as"},
			{"printf '%s' \"$SECRET\" | jf auth -stdin -ticket TICKET slack-smarta", "Store a secret from a script"},
		},
		Notes: []string{
			"A secret is never a command argument, because arguments appear in the process list where any other process on the machine reads them.",
			"The ticket works once, for that one connection, for five minutes. " +
				"The secret never passes through the browser: only the ticket does.",
			"`jf auth` clears this machine's cached copy after a write, so the next read here fetches the value you just stored.",
		},
	},
	"creds": {
		Name:    "creds",
		Summary: "Read one credential from the hub, for scripts",
		Usage: []string{
			"creds get [--no-cache] CONNECTION",
		},
		Description: "`jf creds get` prints one credential to standard output, and nothing else, so a script reads it with a command substitution. " +
			"jf caches the value under `~/.cache/jackfield` for five minutes, and asks the hub again after that. " +
			"This is mostly internal plumbing, exposed for scripts and for a person who debugs a connection.",
		Flags: func(flags *flag.FlagSet) {
			flags.Bool("no-cache", false, "Ask the hub even when a fresh cached copy exists")
		},
		Examples: []example{
			{"jf creds get slack-smarta", "Print the Slack credential"},
			{"jf creds get -no-cache slack-smarta", "Skip the local cache and ask the hub"},
		},
		Notes: []string{
			"The five-minute cache means a credential that `jf auth` replaced reaches every machine within five minutes, with no action on those machines.",
		},
	},
	"version": {
		Name:    "version",
		Summary: "Print the version of jf",
		Usage: []string{
			"version",
			"--version",
		},
		Description: "`jf version` prints the version of this binary. " +
			"A release build prints its tag. A build from source prints `dev`.",
		Examples: []example{
			{"jf version", "Print the version"},
		},
	},
	"help": {
		Name:    "help",
		Summary: "Show this overview, or the help for one command",
		Usage: []string{
			"help",
			"help COMMAND",
		},
		Description: "`jf help` prints the overview of every command. " +
			"`jf help COMMAND` prints what one command does, its flags, and its examples. " +
			"`jf COMMAND -h` prints the same page.",
		Examples: []example{
			{"jf help", "List every command"},
			{"jf help login", "Read the help for one command"},
		},
	},
}

// isHelpRequest reports whether the arguments ask for the overview.
func isHelpRequest(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "help", "--help", "-help", "-h":
		return true
	default:
		return false
	}
}

// wantsCommandHelp reports whether a command's own arguments ask for its help.
func wantsCommandHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "-help":
			return true
		case "--":
			// Everything after `--` belongs to the child command.
			return false
		}
	}
	return false
}

// knownCommands returns every command name, sorted. The unknown-command message
// and the tests both read it.
func knownCommands() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unknownCommandError names a wrong command and points at the overview.
//
// It also offers the closest known name, because a person who types `jf devices`
// as `jf device` wants that one word back, not a list to read.
func unknownCommandError(name string) error {
	if suggestion, found := closestCommand(name); found {
		return fmt.Errorf("unknown command %q. Did you mean `jf %s`? Run `jf help` to see every command", name, suggestion)
	}
	return fmt.Errorf("unknown command %q. Run `jf help` to see every command", name)
}

// closestCommand returns the known command that is nearest to what was typed.
//
// The distance limit is deliberately tight. A suggestion is useful for a typo of
// one or two letters. Past that, a guess is noise, and the person is better
// served by the command list.
func closestCommand(name string) (string, bool) {
	lowered := strings.ToLower(name)
	best := ""
	bestDistance := 3
	for _, candidate := range knownCommands() {
		distance := editDistance(lowered, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best, best != ""
}

// editDistance returns the Levenshtein distance between two words.
func editDistance(left string, right string) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex := 1; leftIndex <= len(left); leftIndex++ {
		current[0] = leftIndex
		for rightIndex := 1; rightIndex <= len(right); rightIndex++ {
			cost := 1
			if left[leftIndex-1] == right[rightIndex-1] {
				cost = 0
			}
			current[rightIndex] = minimum(
				previous[rightIndex]+1,
				current[rightIndex-1]+1,
				previous[rightIndex-1]+cost,
			)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func minimum(values ...int) int {
	smallest := values[0]
	for _, value := range values[1:] {
		if value < smallest {
			smallest = value
		}
	}
	return smallest
}

// printOverview writes the `jf help` page.
func printOverview(out io.Writer) {
	fmt.Fprintf(out, "%s\n\n", tagline)
	fmt.Fprintln(out, "jf answers two questions. Which identity does a CLI get in this directory?")
	fmt.Fprintln(out, "And where does that credential come from? A manifest, jackfield.yaml, answers")
	fmt.Fprintln(out, "the first. The hub answers the second.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "USAGE")
	fmt.Fprintln(out, "  jf [--config PATH] COMMAND [ARGS...]")
	fmt.Fprintln(out)

	for _, group := range commandGroups {
		fmt.Fprintf(out, "%s\n", strings.ToUpper(group.Title))
		if group.Blurb != "" {
			fmt.Fprintf(out, "  %s\n", group.Blurb)
			fmt.Fprintln(out)
		}
		for _, name := range group.Names {
			entry, found := commands[name]
			if !found {
				continue
			}
			fmt.Fprintf(out, "  %-9s %s\n", entry.Name, entry.Summary)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "GLOBAL FLAGS")
	fmt.Fprintln(out, "  --config PATH   Read this manifest instead of searching for jackfield.yaml")
	fmt.Fprintln(out, "  --version       Print the version of jf")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "EXAMPLES")
	fmt.Fprintln(out, "  jf login                              Sign this machine in to the hub")
	fmt.Fprintln(out, "  jf status                             See where every connection stands")
	fmt.Fprintln(out, "  jf resolve gog                        See which Google account gog gets here")
	fmt.Fprintln(out, "  jf run wrangler r2 bucket list        Run wrangler under this workspace's profile")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `jf help COMMAND` to read what one command does, for example `jf help login`.")
}

// printCommandHelp writes the `jf help COMMAND` page.
func printCommandHelp(out io.Writer, entry *command) {
	fmt.Fprintf(out, "jf %s — %s\n\n", entry.Name, firstRuneLower(entry.Summary))
	for _, line := range wrap(stripBackticks(entry.Description), 78) {
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "USAGE")
	for _, usage := range entry.Usage {
		fmt.Fprintf(out, "  jf %s\n", usage)
	}
	fmt.Fprintln(out)

	if flagLines := describeFlags(entry); len(flagLines) > 0 {
		fmt.Fprintln(out, "FLAGS")
		for _, line := range flagLines {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out)
	}

	if len(entry.Examples) > 0 {
		fmt.Fprintln(out, "EXAMPLES")
		for _, item := range entry.Examples {
			fmt.Fprintf(out, "  %s\n", item.Command)
			fmt.Fprintf(out, "      %s\n", item.Comment)
		}
		fmt.Fprintln(out)
	}

	for _, note := range entry.Notes {
		for _, line := range wrap(stripBackticks(note), 78) {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "Run `jf help` to see every command.")
}

// describeFlags renders one line per flag, with its default when it has one.
//
// The lines come from a real flag set, which the command itself also builds, so
// the help text cannot describe a flag that the command does not have.
func describeFlags(entry *command) []string {
	if entry.Flags == nil {
		return nil
	}
	set := flag.NewFlagSet(entry.Name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	entry.Flags(set)

	var lines []string
	set.VisitAll(func(item *flag.Flag) {
		name := "-" + item.Name
		if item.DefValue != "" && item.DefValue != "false" {
			name += " " + item.DefValue
		}
		lines = append(lines, fmt.Sprintf("  %-14s %s", name, stripBackticks(item.Usage)))
	})
	return lines
}

// stripBackticks removes the backticks that mark a literal in the source text.
// The terminal has no way to render them, and they read as noise there.
func stripBackticks(text string) string {
	return strings.ReplaceAll(text, "`", "")
}

// firstRuneLower lowers the first letter of a summary, so it reads as a clause
// after the em dash in the title line.
func firstRuneLower(text string) string {
	if text == "" {
		return text
	}
	return strings.ToLower(text[:1]) + text[1:]
}

// wrap breaks text into lines no longer than width.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	return append(lines, line)
}
