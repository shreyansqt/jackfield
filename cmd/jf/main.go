package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shreyansqt/jackfield/internal/runner"
)

func main() {
	args, err := commandArgs(filepath.Base(os.Args[0]), os.Args[1:])
	if err == nil {
		err = run(args)
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "jf: %v\n", err)
		os.Exit(1)
	}
}

func commandArgs(program string, args []string) ([]string, error) {
	switch program {
	case "gog", "wrangler", "aws":
		result := []string{"run"}
		if len(args) > 0 && args[0] == "--jf-profile" {
			if len(args) < 2 || args[1] == "" {
				return nil, fmt.Errorf("--jf-profile needs a profile name")
			}
			result = append(result, "--profile", args[1])
			args = args[2:]
		}
		result = append(result, program)
		return append(result, args...), nil
	default:
		return args, nil
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("jf", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	configPath := global.String("config", "", "Path to the Jackfield manifest")
	if err := global.Parse(args); err != nil {
		return err
	}
	if global.NArg() == 0 {
		return fmt.Errorf("use jf [--config PATH] run|resolve [--profile NAME] COMMAND [ARGS]")
	}

	manifestPath, err := findConfig(*configPath)
	if err != nil {
		return err
	}
	config, err := runner.Load(manifestPath)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("read working directory: %w", err)
	}

	action := global.Arg(0)
	actionArgs := global.Args()[1:]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	requestedProfile := flags.String("profile", "", "Select one allowed profile")
	if err := flags.Parse(actionArgs); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("%s needs a command", action)
	}
	commandName := flags.Arg(0)
	resolution, commandArgs, err := config.ResolveArgs(cwd, commandName, *requestedProfile, flags.Args()[1:])
	if err != nil {
		return err
	}

	switch action {
	case "resolve":
		fmt.Printf("workspace=%s command=%s profile=%s executable=%s\n", resolution.Workspace, resolution.Command, resolution.Profile, resolution.Launch.Executable)
		return nil
	case "run":
		return resolution.Exec(commandArgs, os.Environ(), os.Stdin, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown action %q; use run or resolve", action)
	}
}

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
	return "", fmt.Errorf("no jackfield.yaml found; set JF_CONFIG or use --config")
}
