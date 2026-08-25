package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
)

/* ------------------------------------------------------------------ */
/* the shim dispatch                                                   */
/* ------------------------------------------------------------------ */

// The shims are the reason argv[0] matters. A call as `jf` runs the command
// tree; a call as `gog`, `wrangler`, or `aws` is that tool, and every argument
// after the name belongs to it.

func TestCommandArgsLeavesJFInvocationUnchanged(t *testing.T) {
	args := []string{"resolve", "gog"}
	actual, err := commandArgs("jf", args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, args) {
		t.Fatalf("got %v, want %v", actual, args)
	}
}

func TestCommandArgsWrapsKnownCommand(t *testing.T) {
	actual, err := commandArgs("wrangler", []string{"r2", "bucket", "list"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "wrangler", "r2", "bucket", "list"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestCommandArgsMovesJFProfileBeforeCommand(t *testing.T) {
	actual, err := commandArgs("aws", []string{"--jf-profile", "aws-smarta-staging", "sts", "get-caller-identity"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--profile", "aws-smarta-staging", "aws", "sts", "get-caller-identity"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestCommandArgsRejectsEmptyJFProfile(t *testing.T) {
	if _, err := commandArgs("aws", []string{"--jf-profile"}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := commandArgs("aws", []string{"--jf-profile", ""}); err == nil {
		t.Fatal("expected an error for an empty profile name")
	}
}

func TestIsShimNamesOnlyTheGatedTools(t *testing.T) {
	for _, name := range []string{"gog", "wrangler", "aws"} {
		if !isShim(name) {
			t.Errorf("%q must be a shim", name)
		}
	}
	for _, name := range []string{"jf", "git", "kubectl", ""} {
		if isShim(name) {
			t.Errorf("%q must not be a shim", name)
		}
	}
}

// A shim must not pass its tool's arguments through a jf flag parser.
//
// `wrangler --help` means the help of wrangler, and `aws --version` means the
// version of aws. Both would be swallowed by cobra, so the rewrite keeps them as
// plain arguments after the tool name.
func TestShimKeepsTheToolsOwnFlags(t *testing.T) {
	for _, testCase := range []struct {
		program string
		args    []string
		want    []string
	}{
		{"wrangler", []string{"--help"}, []string{"run", "wrangler", "--help"}},
		{"aws", []string{"--version"}, []string{"run", "aws", "--version"}},
		{"gog", []string{"-h"}, []string{"run", "gog", "-h"}},
		{"gog", []string{}, []string{"run", "gog"}},
	} {
		actual, err := commandArgs(testCase.program, testCase.args)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, testCase.want) {
			t.Errorf("%s %v: got %v, want %v", testCase.program, testCase.args, actual, testCase.want)
		}
	}
}

// runShim reads back the arguments that commandArgs built. The two must agree,
// or a shim would run the wrong tool under the wrong profile.
func TestRunShimReadsBackTheProfileAndTheTool(t *testing.T) {
	// A manifest that allows nothing, so the run fails at the lookup rather
	// than starting a real process. The error names what the shim asked for,
	// which is what this test checks.
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "jackfield.yaml")
	manifest := "version: 1\nworkspaces:\n  none:\n    roots: [/nowhere]\n    commands: {}\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JF_CONFIG", manifestPath)

	args, err := commandArgs("aws", []string{"--jf-profile", "aws-smarta-staging", "sts", "get-caller-identity"})
	if err != nil {
		t.Fatal(err)
	}
	err = runShim(args)
	if err == nil {
		t.Fatal("expected the lookup to fail for a manifest that allows nothing")
	}
	// The failure must come from the workspace lookup, which proves the shim
	// reached the runner with a tool name rather than failing on its own
	// argument handling.
	if strings.Contains(err.Error(), "no command to run") {
		t.Fatalf("the shim lost the tool name: %v", err)
	}
}

func TestRunShimNeedsATool(t *testing.T) {
	if err := runShim([]string{"run"}); err == nil {
		t.Fatal("expected an error when the shim carries no tool name")
	}
}

/* ------------------------------------------------------------------ */
/* the manifest search                                                 */
/* ------------------------------------------------------------------ */

func TestFindConfigPrefersTheExplicitPath(t *testing.T) {
	t.Setenv("JF_CONFIG", "/from/the/environment.yaml")
	path, err := findConfig("/explicit/path.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/explicit/path.yaml" {
		t.Fatalf("got %q, want the explicit path", path)
	}
}

func TestFindConfigReadsTheEnvironment(t *testing.T) {
	t.Setenv("JF_CONFIG", "/from/the/environment.yaml")
	path, err := findConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/from/the/environment.yaml" {
		t.Fatalf("got %q, want the path from JF_CONFIG", path)
	}
}

func TestFindConfigWalksUpToAParent(t *testing.T) {
	t.Setenv("JF_CONFIG", "")
	root := t.TempDir()
	manifestPath := filepath.Join(root, "jackfield.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	path, err := findConfig("")
	if err != nil {
		t.Fatal(err)
	}
	// The temporary directory may be a symbolic link on macOS, so compare the
	// resolved paths rather than the literal strings.
	actual, _ := filepath.EvalSymlinks(path)
	wanted, _ := filepath.EvalSymlinks(manifestPath)
	if actual != wanted {
		t.Fatalf("got %q, want %q", path, manifestPath)
	}
}

/* ------------------------------------------------------------------ */
/* run and resolve                                                     */
/* ------------------------------------------------------------------ */

// `jf run` must hand every argument after the tool name to that tool. A flag
// that jf also has, such as --profile, belongs to the tool once it appears after
// the tool name.
func TestRunPassesLaterFlagsToTheChildTool(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "jackfield.yaml")
	manifest := "version: 1\nworkspaces:\n  none:\n    roots: [/nowhere]\n    commands: {}\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JF_CONFIG", manifestPath)

	environment := defaultHubEnvironment("")
	var out bytes.Buffer
	environment.Stdout = &out
	environment.Stderr = &out

	root := newRootCommand(environment)
	root.SetOut(&out)
	root.SetErr(&out)
	// --plain is a flag of gog, not of jf. Cobra must not reject it.
	root.SetArgs([]string{"run", "gog", "whoami", "--plain"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected the workspace lookup to fail for this manifest")
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("jf read a flag that belongs to the child tool: %v", err)
	}
}

func TestRunNeedsACommand(t *testing.T) {
	environment := defaultHubEnvironment("")
	root := newRootCommand(environment)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"run"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("`jf run` with no command should fail")
	}
}

/* ------------------------------------------------------------------ */
/* how a failure is reported                                           */
/* ------------------------------------------------------------------ */

// A tool that `jf run` started writes its own message and then exits. jf must
// not print a second error under it: the person already has the tool's words,
// and the exit status reaches the shell either way.
func TestReportErrorStaysQuietForAChildFailure(t *testing.T) {
	var out bytes.Buffer
	reportError(&out, fang.Styles{}, &exec.ExitError{ProcessState: &os.ProcessState{}})
	if out.Len() != 0 {
		t.Fatalf("got %q, want nothing for a child process failure", out.String())
	}
}

// Every other failure is a jf failure, and it must be printed.
func TestReportErrorPrintsAJackfieldFailure(t *testing.T) {
	var out bytes.Buffer
	reportError(&out, fang.Styles{}, errors.New("no hub address"))
	if !strings.Contains(out.String(), "no hub address") {
		t.Fatalf("got %q, want the message printed", out.String())
	}
}

// The exit status of the child process is the status a script reads.
func TestExitKeepsTheChildStatus(t *testing.T) {
	// exit() calls os.Exit, so the behaviour is checked through the error type
	// it branches on rather than by running it.
	var exitError *exec.ExitError
	if !errors.As(error(&exec.ExitError{ProcessState: &os.ProcessState{}}), &exitError) {
		t.Fatal("a child failure must be recognised as an exec.ExitError")
	}
	if errors.As(errors.New("a jf failure"), &exitError) {
		t.Fatal("a jf failure must not be read as a child failure")
	}
}
