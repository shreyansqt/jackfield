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
