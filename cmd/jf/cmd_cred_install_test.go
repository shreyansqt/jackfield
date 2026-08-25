package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// writeGogStub writes a fake gog binary that records its argv and its stdin.
//
// The stub lets the install test run with no real gog and no real account. It
// writes argv to argvFile and stdin to stdinFile, so the test asserts that the
// refresh token arrived on stdin and never in argv.
func writeGogStub(t *testing.T, argvFile string, stdinFile string) string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "gog-stub.sh")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellQuote(argvFile) + "\n" +
		"cat > " + shellQuote(stdinFile) + "\n" +
		"echo 'imported true' 1>&2\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

// shellQuote wraps a path in single quotes for the stub script. The test paths
// come from t.TempDir(), so they carry no single quote.
func shellQuote(path string) string {
	return "'" + path + "'"
}

func gogPersonalSecret() string {
	return `{"refresh_token":"1//0-fake-refresh-token","email":"shreyansqt@gmail.com","client":"default","client_id":"123.apps.googleusercontent.com"}`
}

func TestCredInstallGogImportsTheRefreshToken(t *testing.T) {
	fake := newCommandHub(t)
	fake.credential = hub.Credential{
		Connection: "gog-personal",
		Secret:     gogPersonalSecret(),
		Identity:   "shreyansqt@gmail.com",
	}

	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	argvFile := filepath.Join(t.TempDir(), "argv")
	stdinFile := filepath.Join(t.TempDir(), "stdin")
	stub := writeGogStub(t, argvFile, stdinFile)
	environment.hubEnvironment.LookGog = func() (string, error) { return stub, nil }

	if err := environment.execute(t, "cred", "install", "gog-personal"); err != nil {
		t.Fatalf("install failed: %v\nstderr: %s", err, environment.stderr.String())
	}

	// The refresh token must arrive on stdin, and nowhere else.
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdin)) != "1//0-fake-refresh-token" {
		t.Fatalf("gog received the wrong stdin: %q", string(stdin))
	}

	// The refresh token must never appear in the argument vector, where any other
	// process on the machine could read it from the process list.
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	argvText := string(argv)
	if strings.Contains(argvText, "1//0-fake-refresh-token") {
		t.Fatalf("the refresh token leaked into argv: %q", argvText)
	}
	for _, want := range []string{"auth", "import", "--refresh-token-stdin", "--email", "shreyansqt@gmail.com", "--client", "default"} {
		if !strings.Contains(argvText, want) {
			t.Fatalf("gog argv missing %q; got: %q", want, argvText)
		}
	}
	// The import uses --email, not the global -a. Passing -a alone makes gog fail
	// with "missing flags: --email=STRING".
	for _, line := range strings.Split(strings.TrimSpace(argvText), "\n") {
		if line == "-a" || line == "--account" {
			t.Fatalf("gog argv must not carry the account flag; got: %q", argvText)
		}
	}

	// The person sees one confirmation on stdout, with the account named.
	if out := environment.stdout.String(); !strings.Contains(out, "shreyansqt@gmail.com") {
		t.Fatalf("stdout did not name the account: %q", out)
	}
}

func TestCredInstallRejectsAnUnknownConnection(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	err := environment.execute(t, "cred", "install", "slack-smarta")
	if err == nil {
		t.Fatal("expected an error for a connection with no installer")
	}
	if !strings.Contains(err.Error(), "no installer") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestCredInstallRejectsMalformedGogJSON(t *testing.T) {
	fake := newCommandHub(t)
	fake.credential = hub.Credential{
		Connection: "gog-personal",
		Secret:     "not-json-at-all",
	}
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)
	environment.hubEnvironment.LookGog = func() (string, error) { return "/does/not/matter", nil }

	err := environment.execute(t, "cred", "install", "gog-personal")
	if err == nil {
		t.Fatal("expected an error for a credential that is not the expected JSON")
	}
	if !strings.Contains(err.Error(), "expected JSON") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestCredInstallRejectsMissingRefreshToken(t *testing.T) {
	fake := newCommandHub(t)
	fake.credential = hub.Credential{
		Connection: "gog-personal",
		Secret:     `{"email":"shreyansqt@gmail.com"}`,
	}
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)
	environment.hubEnvironment.LookGog = func() (string, error) { return "/does/not/matter", nil }

	err := environment.execute(t, "cred", "install", "gog-personal")
	if err == nil || !strings.Contains(err.Error(), "no refresh_token") {
		t.Fatalf("expected a missing-refresh-token error, got: %v", err)
	}
}

func TestFindRealGogHonorsGogBinOverride(t *testing.T) {
	dir := t.TempDir()
	realGog := filepath.Join(dir, "gog")
	if err := os.WriteFile(realGog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findRealGog(realGog)
	if err != nil {
		t.Fatalf("override should win: %v", err)
	}
	if got != realGog {
		t.Fatalf("expected %q, got %q", realGog, got)
	}
}

func TestFindRealGogRejectsNonExecutableOverride(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "gog")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := findRealGog(notExec)
	if err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("expected a not-executable error, got: %v", err)
	}
}

func TestFindRealGogSkipsTheShimOnPath(t *testing.T) {
	// Build a fake install: a jf binary, and a "gog" shim that symlinks to it,
	// in a bin dir put first on PATH. With no GOG_BIN and no Homebrew gog, the
	// only gog on PATH is the shim, so findRealGog must refuse rather than return
	// it. This is the exact live bug: the shim denies -a.
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jfBinary := filepath.Join(libDir, "jf")
	if err := os.WriteFile(jfBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gogShim := filepath.Join(binDir, "gog")
	if err := os.Symlink(jfBinary, gogShim); err != nil {
		t.Fatal(err)
	}

	// PATH holds only the shim dir, so LookPath finds the shim and nothing else.
	t.Setenv("PATH", binDir)

	// The empty Homebrew path forces the PATH branch, so the shim-skip logic is
	// tested even on a machine that has a real Homebrew gog.
	_, err := findRealGogIn("", "")
	if err == nil {
		t.Fatal("expected findRealGog to refuse the shim")
	}
	if !strings.Contains(err.Error(), "shim") {
		t.Fatalf("expected a shim-refusal error, got: %v", err)
	}

	// A GOG_BIN override still wins even with the shim first on PATH.
	realGog := filepath.Join(root, "real-gog")
	if err := os.WriteFile(realGog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findRealGogIn(realGog, "")
	if err != nil {
		t.Fatalf("override should win over the shim: %v", err)
	}
	if got != realGog {
		t.Fatalf("expected the override %q, got %q", realGog, got)
	}
}

func TestIsJackfieldShimDetectsTheSymlink(t *testing.T) {
	root := t.TempDir()
	jfBinary := filepath.Join(root, "jf")
	if err := os.WriteFile(jfBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "gog")
	if err := os.Symlink(jfBinary, shim); err != nil {
		t.Fatal(err)
	}
	if !isJackfieldShim(shim) {
		t.Fatal("a symlink to the jf binary must read as a shim")
	}

	realGog := filepath.Join(root, "real-gog")
	if err := os.WriteFile(realGog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isJackfieldShim(realGog) {
		t.Fatal("a real binary must not read as a shim")
	}
}

func TestGogImportArgsUsesEmailNotAccountFlag(t *testing.T) {
	args := gogImportArgs(gogSecret{Email: "shreyansqt@gmail.com", Client: "default"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--email shreyansqt@gmail.com") {
		t.Fatalf("expected the required --email flag: %q", joined)
	}
	for _, arg := range args {
		if arg == "-a" || arg == "--account" {
			t.Fatalf("the import must not use the global account flag: %q", joined)
		}
	}
}

func TestGogImportArgsOmitsClientWhenEmpty(t *testing.T) {
	args := gogImportArgs(gogSecret{Email: "shreyansqt@gmail.com"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--client") {
		t.Fatalf("expected no --client when Client is empty: %q", joined)
	}
	if !strings.Contains(joined, "--email shreyansqt@gmail.com") {
		t.Fatalf("expected the required --email flag: %q", joined)
	}
}
