package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIsVersionRequestAcceptsEveryForm(t *testing.T) {
	for _, argument := range []string{"--version", "-version", "-v", "version"} {
		if !isVersionRequest([]string{argument}) {
			t.Fatalf("expected %q to ask for the version", argument)
		}
	}
}

func TestIsVersionRequestRejectsOtherArguments(t *testing.T) {
	cases := [][]string{
		{},
		{"run", "gog"},
		{"--version", "extra"},
		{"status"},
	}
	for _, args := range cases {
		if isVersionRequest(args) {
			t.Fatalf("expected %v not to ask for the version", args)
		}
	}
}

func TestPrintVersionUsesTheLinkerValue(t *testing.T) {
	previous := version
	version = "v1.2.3"
	defer func() { version = previous }()

	var out bytes.Buffer
	printVersion(&out)
	if out.String() != "jf v1.2.3\n" {
		t.Fatalf("got %q, want %q", out.String(), "jf v1.2.3\n")
	}
}

func TestRunAnswersVersionWithoutAManifest(t *testing.T) {
	// `jf --version` is the first command a person runs after install.sh, on a
	// machine that has no jackfield.yaml. It must not fail there.
	t.Setenv("JF_CONFIG", "/nonexistent/jackfield.yaml")
	if err := run([]string{"--version"}); err != nil {
		t.Fatalf("jf --version failed: %v", err)
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
