package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jackfield.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBaseURLReadsTheManifest(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	path := writeManifest(t, "version: 1\nhub: https://hub.example.com\n")

	baseURL, err := BaseURL(path)
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://hub.example.com" {
		t.Fatalf("got %q, want https://hub.example.com", baseURL)
	}
}

// The environment variable wins, so a person can point one shell at a second
// deployment without editing the manifest every machine shares.
func TestBaseURLPrefersTheEnvironmentVariable(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://other-hub.example.com")
	path := writeManifest(t, "version: 1\nhub: https://hub.example.com\n")

	baseURL, err := BaseURL(path)
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://other-hub.example.com" {
		t.Fatalf("got %q, want https://other-hub.example.com", baseURL)
	}
}

// A fresh machine runs `jf login` before it has any manifest.
func TestBaseURLWorksWithNoManifest(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://hub.example.com")
	baseURL, err := BaseURL("")
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://hub.example.com" {
		t.Fatalf("got %q, want https://hub.example.com", baseURL)
	}
}

func TestBaseURLRemovesATrailingSlash(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://hub.example.com/")
	baseURL, err := BaseURL("")
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://hub.example.com" {
		t.Fatalf("got %q, want no trailing slash", baseURL)
	}
}

func TestBaseURLNamesBothWaysToSetIt(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	path := writeManifest(t, "version: 1\n")

	_, err := BaseURL(path)
	if err == nil {
		t.Fatal("expected an error for a manifest with no hub key")
	}
	if !strings.Contains(err.Error(), "hub:") || !strings.Contains(err.Error(), EnvBaseURL) {
		t.Fatalf("got %q, want both the manifest key and the environment variable", err)
	}
}

func TestBaseURLRejectsAnAddressWithNoScheme(t *testing.T) {
	t.Setenv(EnvBaseURL, "hub.example.com")
	if _, err := BaseURL(""); err == nil {
		t.Fatal("expected an error for an address with no scheme")
	}
}

// The runner decoder sets KnownFields(true) and would reject a hub key. This
// decoder ignores every key it does not know, so both read the same file.
func TestBaseURLIgnoresTheRestOfTheManifest(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	path := writeManifest(t, `version: 1
hub: https://hub.example.com
workspaces:
  side-projects:
    roots: [/Users/someone/workspaces/side-projects]
    commands:
      gog:
        profiles: [gog-personal]
profiles:
  gog-personal:
    executable: /opt/homebrew/bin/gog
`)

	baseURL, err := BaseURL(path)
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://hub.example.com" {
		t.Fatalf("got %q, want https://hub.example.com", baseURL)
	}
}

func TestTokenPathAndCacheDirFollowTheirOverrides(t *testing.T) {
	t.Setenv("JF_TOKEN_FILE", "/tmp/jf-token")
	t.Setenv("JF_CACHE_DIR", "/tmp/jf-cache")

	tokenPath, err := TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	if tokenPath != "/tmp/jf-token" {
		t.Fatalf("got %q, want /tmp/jf-token", tokenPath)
	}

	cacheDirectory, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if cacheDirectory != "/tmp/jf-cache" {
		t.Fatalf("got %q, want /tmp/jf-cache", cacheDirectory)
	}
}

func TestTokenPathDefaultsUnderTheConfigDirectory(t *testing.T) {
	t.Setenv("JF_TOKEN_FILE", "")
	t.Setenv("HOME", "/Users/someone")

	tokenPath, err := TokenPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/Users/someone", ".config", "jackfield", "device-token")
	if tokenPath != want {
		t.Fatalf("got %q, want %q", tokenPath, want)
	}
}
