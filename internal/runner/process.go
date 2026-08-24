package runner

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func (resolution Resolution) Exec(args []string, baseEnv []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	prefixArgs, err := resolution.Launch.launchPrefix(args)
	if err != nil {
		return err
	}
	commandArgs := append(append([]string{}, prefixArgs...), args...)
	command := exec.Command(resolution.Launch.Executable, commandArgs...)
	command.Env = BuildEnv(baseEnv, resolution.Launch.UnsetEnv, resolution.Launch.Env)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

// launchPrefix validates the child arguments and returns the prefix arguments for
// them. A command that matches an interactive rule keeps its denied-argument checks,
// must name the pinned identity, and loses the prefix arguments that block prompts.
func (profile Profile) launchPrefix(args []string) ([]string, error) {
	if err := ValidateArgs(args, profile.DeniedArgs); err != nil {
		return nil, err
	}
	rule, found := profile.interactiveRule(args)
	if !found {
		return profile.PrefixArgs, nil
	}
	if err := rule.validateIdentity(args); err != nil {
		return nil, err
	}
	return removeArgs(profile.PrefixArgs, rule.DropPrefixArgs), nil
}

// interactiveRule returns the first rule whose subcommand words start the command.
// Flags before and between those words are ignored, so `gog --verbose auth add`
// matches the `auth add` rule.
func (profile Profile) interactiveRule(args []string) (Interactive, bool) {
	for _, rule := range profile.Interactive {
		if matchesSubcommand(args, rule.Subcommand) {
			return rule, true
		}
	}
	return Interactive{}, false
}

func matchesSubcommand(args []string, subcommand []string) bool {
	matched := 0
	for _, arg := range args {
		if matched == len(subcommand) {
			return true
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if arg != subcommand[matched] {
			return false
		}
		matched++
	}
	return matched == len(subcommand)
}

// validateIdentity rejects an interactive command that names an account other than
// the pinned one. gog takes the account as a positional argument, for example
// `gog auth add someone@example.com`, so every word after the subcommand counts.
func (rule Interactive) validateIdentity(args []string) error {
	if rule.Identity == "" {
		return nil
	}
	matched := 0
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if matched < len(rule.Subcommand) {
			matched++
			continue
		}
		if arg != rule.Identity {
			return fmt.Errorf("%s is pinned to %q; it cannot run for %q", strings.Join(rule.Subcommand, " "), rule.Identity, arg)
		}
	}
	return nil
}

func removeArgs(args []string, unwanted []string) []string {
	drop := make(map[string]bool, len(unwanted))
	for _, name := range unwanted {
		drop[name] = true
	}
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if !drop[arg] {
			result = append(result, arg)
		}
	}
	return result
}

func ValidateArgs(args []string, denied []string) error {
	for _, arg := range args {
		for _, blocked := range denied {
			shortForm := len(blocked) == 2 && strings.HasPrefix(blocked, "-") && strings.HasPrefix(arg, blocked)
			if arg == blocked || strings.HasPrefix(arg, blocked+"=") || shortForm {
				return fmt.Errorf("argument %q can override the selected identity", arg)
			}
		}
	}
	return nil
}

func BuildEnv(base []string, unset []string, replacements map[string]string) []string {
	blocked := make(map[string]bool, len(unset)+len(replacements))
	for _, name := range unset {
		blocked[name] = true
	}
	for name := range replacements {
		blocked[name] = true
	}

	result := make([]string, 0, len(base)+len(replacements))
	for _, item := range base {
		name := item
		for index, character := range item {
			if character == '=' {
				name = item[:index]
				break
			}
		}
		if !blocked[name] {
			result = append(result, item)
		}
	}
	for name, value := range replacements {
		result = append(result, name+"="+value)
	}
	return result
}
