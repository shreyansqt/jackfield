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
