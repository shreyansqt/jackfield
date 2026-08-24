package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveTokenWritesAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "jackfield", "device-token")
	if err := SaveToken(path, "jfd_the-device-token"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the token file has mode %04o, want 0600", mode)
	}

	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := directory.Mode().Perm(); mode != 0o700 {
		t.Fatalf("the token directory has mode %04o, want 0700", mode)
	}
}

func TestLoadTokenReturnsTheSavedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-token")
	if err := SaveToken(path, "jfd_the-device-token"); err != nil {
		t.Fatal(err)
	}

	token, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if token != "jfd_the-device-token" {
		t.Fatalf("got token %q, want jfd_the-device-token", token)
	}
}

func TestSaveTokenReplacesAnEarlierToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-token")
	if err := SaveToken(path, "jfd_first"); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(path, "jfd_second"); err != nil {
		t.Fatal(err)
	}

	token, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if token != "jfd_second" {
		t.Fatalf("got token %q, want jfd_second", token)
	}
	// The temporary file must not survive the rename.
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatal("the temporary file must be gone after the rename")
	}
}

func TestLoadTokenTellsThePersonToRunLogin(t *testing.T) {
	_, err := LoadToken(filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("expected an error for a machine with no token")
	}
	if !strings.Contains(err.Error(), "jf login") {
		t.Fatalf("got %q, want a message naming `jf login`", err)
	}
}

// A device token reads every credential in the hub, so a file that other users
// can read is an error rather than a warning.
func TestLoadTokenRefusesAWorldReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-token")
	if err := os.WriteFile(path, []byte("jfd_leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadToken(path)
	if err == nil {
		t.Fatal("expected an error for a token file other users can read")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("got %q, want the command that fixes it", err)
	}
}

func TestLoadTokenRefusesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-token")
	if err := os.WriteFile(path, []byte("\n  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("expected an error for an empty token file")
	}
}
