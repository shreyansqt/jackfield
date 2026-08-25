package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionStringUsesTheLinkerValue(t *testing.T) {
	previous := version
	version = "v1.2.3"
	defer func() { version = previous }()

	if versionString() != "v1.2.3" {
		t.Fatalf("got %q, want v1.2.3", versionString())
	}
}

func TestVersionStringFallsBackToBuildInfo(t *testing.T) {
	previous := version
	version = "dev"
	defer func() { version = previous }()

	// A test binary carries build info, so this returns either a recorded
	// module version or the "dev" fallback. Neither may be empty.
	if strings.TrimSpace(versionString()) == "" {
		t.Fatal("expected a non-empty version")
	}
}

// `jf --version` is the first command a person runs after install.sh, on a
// machine that has no jackfield.yaml. It must not fail there.
func TestVersionAnswersWithoutAManifest(t *testing.T) {
	t.Setenv("JF_CONFIG", "/nonexistent/jackfield.yaml")

	var out bytes.Buffer
	environment := defaultHubEnvironment("")
	environment.Stdout = &out
	root := newRootCommand(environment)
	root.Version = versionString()
	root.SetArgs([]string{"--version"})
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("jf --version failed: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("jf --version printed nothing")
	}
}

// The bare word must answer as well as the flag. A person types `jf version` out
// of habit, and the older documents name it.
func TestVersionSubcommandPrintsTheVersion(t *testing.T) {
	previous := version
	version = "v1.2.3"
	defer func() { version = previous }()

	var out bytes.Buffer
	environment := defaultHubEnvironment("")
	environment.Stdout = &out
	root := newRootCommand(environment)
	root.SetArgs([]string{"version"})
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Fatalf("got %q, want the version printed", out.String())
	}
}

// The two forms must agree. A tool that answers one and not the other, or that
// answers them differently, makes the reader guess which one it takes.
func TestVersionSubcommandMatchesTheFlag(t *testing.T) {
	previous := version
	version = "v9.9.9"
	defer func() { version = previous }()

	read := func(args ...string) string {
		var out bytes.Buffer
		environment := defaultHubEnvironment("")
		environment.Stdout = &out
		root := newRootCommand(environment)
		// fang sets this from the same versionString() in main.
		root.Version = "jf version " + versionString()
		root.SetVersionTemplate("{{.Version}}\n")
		root.SetArgs(args)
		root.SetOut(&out)
		root.SetErr(&out)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}

	fromWord := read("version")
	fromFlag := read("--version")
	if fromWord != fromFlag {
		t.Fatalf("`jf version` printed %q but `jf --version` printed %q; the two forms must agree", fromWord, fromFlag)
	}
}

// A machine with no manifest must still answer the bare word.
func TestVersionSubcommandNeedsNoManifest(t *testing.T) {
	t.Setenv("JF_CONFIG", "/nonexistent/jackfield.yaml")

	var out bytes.Buffer
	environment := defaultHubEnvironment("")
	environment.Stdout = &out
	root := newRootCommand(environment)
	root.SetArgs([]string{"version"})
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("jf version failed with no manifest: %v", err)
	}
}
