package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// schemaCommand is one command in the machine-readable command tree.
//
// The shape is deliberately flat and obvious. An agent reads it once and knows
// every command, every flag, and every argument that jf accepts.
type schemaCommand struct {
	// Name is the word a person types, for example "status".
	Name string `json:"name"`
	// Path is the full command line up to this command, for example
	// "jf device revoke".
	Path string `json:"path"`
	// Summary is the one-line description.
	Summary string `json:"summary"`
	// Description is the full text of the help page.
	Description string `json:"description,omitempty"`
	// Usage is the usage line, for example "revoke NAME".
	Usage string `json:"usage"`
	// Arguments describes the positional arguments this command takes.
	Arguments []schemaArgument `json:"arguments,omitempty"`
	// Flags describes this command's own flags.
	Flags []schemaFlag `json:"flags,omitempty"`
	// InheritedFlags describes the flags that a parent command supplies.
	InheritedFlags []schemaFlag `json:"inherited_flags,omitempty"`
	// Examples is the text of the help page's example block.
	Examples string `json:"examples,omitempty"`
	// Commands holds the subcommands of this command.
	Commands []schemaCommand `json:"commands,omitempty"`
}

// schemaFlag is one flag of one command.
type schemaFlag struct {
	// Name is the long name, without the leading dashes.
	Name string `json:"name"`
	// Shorthand is the single-letter name, when the flag has one.
	Shorthand string `json:"shorthand,omitempty"`
	// Type is the value the flag takes: "bool", "string", and so on.
	Type string `json:"type"`
	// Default is the value the flag has when nobody sets it.
	Default string `json:"default,omitempty"`
	// Usage says what the flag does.
	Usage string `json:"usage"`
}

// schemaArgument is one positional argument of one command.
type schemaArgument struct {
	// Name is the placeholder from the usage line, for example "NAME".
	Name string `json:"name"`
	// Variadic reports whether this argument accepts more than one value.
	Variadic bool `json:"variadic"`
}

// schemaDocument is the whole answer of `jf schema --json`.
type schemaDocument struct {
	// Tool is always "jf".
	Tool string `json:"tool"`
	// Version is the version of this binary.
	Version string `json:"version"`
	// Description is the overview text of the root command.
	Description string `json:"description"`
	// Commands is the command tree.
	Commands []schemaCommand `json:"commands"`
}

func newSchemaCommand() *cobra.Command {
	var asJSON bool

	command := &cobra.Command{
		Use:   "schema",
		Short: "Print the whole command tree as JSON, for an agent",
		Long: `Print every command, description, flag, and argument as JSON.

The document is generated from the command tree itself, so it cannot describe a
command that jf does not have, and it cannot miss a command that jf does have.

An agent that reads "jf --help" and "jf schema --json" can operate jf with no
other document. The hidden commands are left out, because they are the aliases
that a new caller should not learn.`,
		Example: `  # Print the command tree.
  jf schema --json

  # List every command path.
  jf schema --json | jq -r '.. | .path? // empty'`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			// --json is the only form today. Accepting the flag now means a
			// later format needs no change at the call sites that already pass
			// it, and an agent that copies the documented line keeps working.
			if !asJSON {
				return fmt.Errorf("`jf schema` prints JSON only. Run `jf schema --json`")
			}
			root := command.Root()
			document := schemaDocument{
				Tool:        root.Name(),
				Version:     versionString(),
				Description: root.Long,
				Commands:    describeChildren(root),
			}
			encoder := json.NewEncoder(command.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(document)
		},
	}

	command.Flags().BoolVar(&asJSON, "json", false, "Print the command tree as JSON")
	return command
}

// describeChildren renders every subcommand of one command that an agent needs.
//
// Two kinds are skipped. A hidden command is one of the deprecated aliases, and
// the schema is what a new caller learns from, so it must teach the current names
// only. "help" and "completion" come from cobra: they describe the shell rather
// than jackfield, and the completion subtree alone is longer than the rest of the
// document put together.
//
// `jf man` is not skipped. jf supplies that command, and an agent that has to
// write the page to a file needs to find it here.
func describeChildren(parent *cobra.Command) []schemaCommand {
	var described []schemaCommand
	for _, child := range parent.Commands() {
		if child.Hidden || isCobraCommand(child.Name()) {
			continue
		}
		described = append(described, describeCommand(child))
	}
	return described
}

// isCobraCommand reports whether cobra supplies this command rather than jf.
func isCobraCommand(name string) bool {
	switch name {
	case "help", "completion":
		return true
	default:
		return false
	}
}

func describeCommand(command *cobra.Command) schemaCommand {
	described := schemaCommand{
		Name:           command.Name(),
		Path:           command.CommandPath(),
		Summary:        command.Short,
		Description:    command.Long,
		Usage:          command.UseLine(),
		Arguments:      describeArguments(command),
		Flags:          describeFlagSet(command.NonInheritedFlags()),
		InheritedFlags: describeFlagSet(command.InheritedFlags()),
		Examples:       command.Example,
		Commands:       describeChildren(command),
	}
	return described
}

// describeFlagSet renders every flag of one flag set.
func describeFlagSet(flags *pflag.FlagSet) []schemaFlag {
	var described []schemaFlag
	flags.VisitAll(func(item *pflag.Flag) {
		// --help is on every command, and it does what it does everywhere. It
		// would triple the flag lists and teach an agent nothing.
		if item.Hidden || item.Name == "help" {
			return
		}
		described = append(described, schemaFlag{
			Name:      item.Name,
			Shorthand: item.Shorthand,
			Type:      item.Value.Type(),
			Default:   item.DefValue,
			// The usage text may carry backticks, which pflag reads as the name
			// of the flag's value. The schema wants the plain words.
			Usage: strings.ReplaceAll(item.Usage, "`", ""),
		})
	})
	return described
}

// describeArguments reads the positional arguments out of the usage line.
//
// Cobra keeps no structured list of them, so the usage line is the only source.
// A placeholder is a word in upper case, which is the convention every command in
// this tree follows: "revoke NAME", "run COMMAND [ARGS...]".
func describeArguments(command *cobra.Command) []schemaArgument {
	fields := strings.Fields(command.Use)
	if len(fields) < 2 {
		return nil
	}

	var arguments []schemaArgument
	for _, field := range fields[1:] {
		bare := strings.Trim(field, "[]")
		variadic := strings.HasSuffix(bare, "...")
		bare = strings.TrimSuffix(bare, "...")
		if bare == "" || bare != strings.ToUpper(bare) {
			continue
		}
		arguments = append(arguments, schemaArgument{Name: bare, Variadic: variadic})
	}
	return arguments
}
