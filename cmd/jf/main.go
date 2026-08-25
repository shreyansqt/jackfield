package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/fang"
)

func main() {
	program := filepath.Base(os.Args[0])

	// A shim runs the runner directly and never reaches cobra.
	//
	// The shims are symbolic links named gog, wrangler, and aws. A person who
	// types `wrangler r2 bucket list` means that tool, not a jf subcommand, so
	// the argument list must not pass through a flag parser that would read
	// `--help` or `--version` as its own. The rewrite below turns the call into
	// the `jf run` that it means, and calls the runner with it.
	if isShim(program) {
		shimArgs, err := commandArgs(program, os.Args[1:])
		if err == nil {
			err = runShim(shimArgs)
		}
		fail(err)
		return
	}

	environment := defaultHubEnvironment("")
	root := newRootCommand(environment)
	if err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(versionString()),
		// jf supplies its own `man` command, with real help text.
		fang.WithoutManpage(),
		fang.WithErrorHandler(reportError),
		fang.WithNotifySignal(os.Interrupt),
	); err != nil {
		// The error is already printed. This path only sets the exit status, and
		// it keeps the child process's status when the failure came from
		// `jf run`.
		exit(err)
	}
}

// reportError prints a failure, unless the child process already reported it.
//
// A tool that `jf run` started writes its own message to its own standard error
// and then exits. jf adds nothing by printing "Exit status 3" under that: the
// person has the tool's own words, and the exit status reaches the shell either
// way. Every other failure is a jf failure, and fang prints it in its own style.
func reportError(out io.Writer, styles fang.Styles, err error) {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return
	}
	fang.DefaultErrorHandler(out, styles, err)
}

// fail ends the program when err is not nil.
func fail(err error) {
	if err == nil {
		return
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		os.Exit(exitError.ExitCode())
	}
	fmt.Fprintf(os.Stderr, "jf: %v\n", err)
	os.Exit(1)
}

// exit ends the program with the right status for an error fang already printed.
//
// A failure inside `jf run` carries the child process's exit status, and a script
// that calls jf reads that status as the tool's own result. Every other failure
// is a jf failure, and it exits 1.
func exit(err error) {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		os.Exit(exitError.ExitCode())
	}
	os.Exit(1)
}

// isShim reports whether this program name is one of the installed shims.
//
// The shims are symbolic links to the jf binary, named for the tool they gate.
// A call as `jf` is not a shim, and it runs the command tree instead.
func isShim(program string) bool {
	switch program {
	case "gog", "wrangler", "aws":
		return true
	default:
		return false
	}
}

// commandArgs rewrites a shim invocation into the `jf run` that it means.
//
// The `--jf-profile NAME` flag is the one jf flag a shim accepts. It must move in
// front of the tool name, because everything after that name belongs to the tool.
// So `wrangler r2 bucket list` becomes `run wrangler r2 bucket list`, and `aws
// --jf-profile P sts get-caller-identity` becomes `run --profile P aws sts
// get-caller-identity`.
func commandArgs(program string, args []string) ([]string, error) {
	if !isShim(program) {
		return args, nil
	}
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
}

// runShim runs one shim call through the runner.
//
// It bypasses cobra on purpose. The arguments after the tool name belong to that
// tool, and a flag parser here would read them as flags of jf.
func runShim(args []string) error {
	profile := ""
	rest := args[1:] // Drop the leading "run".
	if len(rest) >= 2 && rest[0] == "--profile" {
		profile = rest[1]
		rest = rest[2:]
	}
	if len(rest) == 0 {
		return fmt.Errorf("the shim received no command to run")
	}

	resolution, childArgs, err := resolveCommand(func() (string, error) { return findConfig("") }, rest[0], profile, rest[1:])
	if err != nil {
		return err
	}
	return resolution.Exec(childArgs, os.Environ(), os.Stdin, os.Stdout, os.Stderr)
}
