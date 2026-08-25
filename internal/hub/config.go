// Package hub is the client for the jackfield hub.
//
// The hub holds every credential. This machine is a cache. Each command in this
// package reads or writes through the hub over HTTPS, with one device token that
// `jf login` obtains.
package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvBaseURL names the environment variable that overrides the manifest.
const EnvBaseURL = "JF_HUB"

// manifestHub is the part of jackfield.yaml that this package reads.
//
// The manifest is decoded again here, with a separate type, rather than through
// runner.Config. The runner decoder sets KnownFields(true), so it rejects a
// manifest that carries a hub key. This type ignores every other key instead, so
// the two decoders do not have to agree about the whole file.
type manifestHub struct {
	Hub string `yaml:"hub"`
}

// BaseURL returns the hub address for this machine.
//
// The order is deliberate. The environment variable wins, because a person who
// exports JF_HUB wants that hub for this shell, usually to test a second
// deployment. The manifest holds the normal address, so every machine that reads
// the same manifest reaches the same hub with no further setup.
//
// manifestPath may be empty. A machine can hold a device token and reach the hub
// without any manifest at all, which is what a fresh `jf login` does.
func BaseURL(manifestPath string) (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvBaseURL)); fromEnv != "" {
		return normalizeBaseURL(fromEnv)
	}
	if manifestPath == "" {
		return "", missingHubError()
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", missingHubError()
	}
	var manifest manifestHub
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	if strings.TrimSpace(manifest.Hub) == "" {
		return "", missingHubError()
	}
	return normalizeBaseURL(manifest.Hub)
}

func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return "", fmt.Errorf("hub address %q must start with https://", raw)
	}
	return trimmed, nil
}

func missingHubError() error {
	return fmt.Errorf("no hub address. Set it for every shell with a hub: key in jackfield.yaml, "+
		"or for this shell with %s=https://your-hub.example.workers.dev", EnvBaseURL)
}

// TokenPath returns the file that holds this machine's device token.
//
// The token is a file under ~/.config/jackfield, not a keychain item. The Mac
// mini runs headless, and the macOS keychain needs an unlocked login session, so
// a keychain item is unreadable there. A file with mode 0600 works on every
// machine.
func TokenPath() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv("JF_TOKEN_FILE")); fromEnv != "" {
		return fromEnv, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".config", "jackfield", "device-token"), nil
}

// CacheDir returns the directory that holds cached credentials.
func CacheDir() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv("JF_CACHE_DIR")); fromEnv != "" {
		return fromEnv, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "jackfield"), nil
}
