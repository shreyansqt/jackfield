package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveToken writes the device token to path with mode 0600.
//
// The write goes to a temporary file first, then renames it over the target. A
// rename is atomic, so a second process never reads a half-written token. The
// temporary file is created with mode 0600 from the start, so the token is never
// readable by another user, not even for the moment between create and chmod.
func SaveToken(path string, token string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", directory, err)
	}

	temporary, err := os.OpenFile(path+".new", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := temporary.WriteString(strings.TrimSpace(token) + "\n"); err != nil {
		temporary.Close()
		os.Remove(temporary.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporary.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		os.Remove(temporary.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadToken reads the device token from path.
//
// It refuses a token file that other users can read. A leaked device token reads
// every credential in the hub, so a wrong file mode is an error, not a warning.
func LoadToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("this machine has no device token; run `jf login`")
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return "", fmt.Errorf("%s has mode %04o; other users can read it. Run `chmod 600 %s`, then `jf login` again", path, mode, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("%s is empty; run `jf login`", path)
	}
	return token, nil
}
