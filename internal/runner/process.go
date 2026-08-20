package runner

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func (resolution Resolution) Exec(args []string, baseEnv []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	if err := ValidateArgs(args, resolution.Launch.DeniedArgs); err != nil {
		return err
	}
	commandArgs := append(append([]string{}, resolution.Launch.PrefixArgs...), args...)
	command := exec.Command(resolution.Launch.Executable, commandArgs...)
	command.Env = BuildEnv(baseEnv, resolution.Launch.UnsetEnv, resolution.Launch.Env)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
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
