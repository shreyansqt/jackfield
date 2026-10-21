package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

type Profile struct {
	Name         string
	AllowedRoots []string `json:"allowed_roots"`
	// TokenFile is optional. It names a file that holds a secret for clients, such as
	// Cursor, that cannot report their workspace. The secret must be 32 to 256 bytes.
	TokenFile string `json:"token_file"`
	token     []byte
}

const (
	minTokenLength = 32
	maxTokenLength = 256
)

func loadToken(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("token file path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("token file must not be readable by group or others (use chmod 600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	token := []byte(strings.TrimSpace(string(data)))
	if len(token) < minTokenLength || len(token) > maxTokenLength {
		return nil, fmt.Errorf("token must be %d to %d bytes", minTokenLength, maxTokenLength)
	}
	return token, nil
}

func LoadPolicy(path string, profileName string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read %s: %w", path, err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Profile{}, fmt.Errorf("parse %s: %w", path, err)
	}

	profile, ok := config.Profiles[profileName]
	if !ok {
		return Profile{}, fmt.Errorf("profile %q does not exist", profileName)
	}
	profile.Name = profileName
	if len(profile.AllowedRoots) == 0 {
		return Profile{}, fmt.Errorf("profile %q has no allowed roots", profileName)
	}

	for index, root := range profile.AllowedRoots {
		canonical, err := canonicalPath(root)
		if err != nil {
			return Profile{}, fmt.Errorf("allowed root %q: %w", root, err)
		}
		profile.AllowedRoots[index] = canonical
	}
	if profile.TokenFile != "" {
		token, err := loadToken(profile.TokenFile)
		if err != nil {
			return Profile{}, fmt.Errorf("token file %q: %w", profile.TokenFile, err)
		}
		profile.token = token
	}
	return profile, nil
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}

	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return clean, nil
	}
	return "", err
}

func pathIsInside(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (profile Profile) Allows(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		allowed := false
		for _, root := range profile.AllowedRoots {
			if pathIsInside(path, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}
