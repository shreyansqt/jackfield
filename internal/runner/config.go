package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version    int                  `yaml:"version"`
	Workspaces map[string]Workspace `yaml:"workspaces"`
	Profiles   map[string]Profile   `yaml:"profiles"`
}

type Workspace struct {
	Roots    []string                    `yaml:"roots"`
	Commands map[string]CommandSelection `yaml:"commands"`
}

type CommandSelection struct {
	Profiles []string `yaml:"profiles"`
	Default  string   `yaml:"default,omitempty"`
}

type Profile struct {
	Executable string            `yaml:"executable"`
	PrefixArgs []string          `yaml:"prefix_args,omitempty"`
	DeniedArgs []string          `yaml:"denied_args,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	UnsetEnv   []string          `yaml:"unset_env,omitempty"`
}

type Resolution struct {
	Workspace string
	Command   string
	Profile   string
	Launch    Profile
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := config.validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return config, nil
}

func (config Config) validate() error {
	if config.Version != 1 {
		return fmt.Errorf("version must be 1")
	}
	if len(config.Workspaces) == 0 || len(config.Profiles) == 0 {
		return fmt.Errorf("workspaces and profiles must not be empty")
	}
	for name, profile := range config.Profiles {
		if strings.TrimSpace(profile.Executable) == "" {
			return fmt.Errorf("profile %q has no executable", name)
		}
	}
	for workspaceName, workspace := range config.Workspaces {
		if len(workspace.Roots) == 0 {
			return fmt.Errorf("workspace %q has no roots", workspaceName)
		}
		for commandName, selection := range workspace.Commands {
			if len(selection.Profiles) == 0 {
				return fmt.Errorf("workspace %q command %q has no profiles", workspaceName, commandName)
			}
			allowed := make(map[string]bool, len(selection.Profiles))
			for _, profileName := range selection.Profiles {
				if _, ok := config.Profiles[profileName]; !ok {
					return fmt.Errorf("workspace %q command %q uses unknown profile %q", workspaceName, commandName, profileName)
				}
				allowed[profileName] = true
			}
			if selection.Default != "" && !allowed[selection.Default] {
				return fmt.Errorf("workspace %q command %q has disallowed default %q", workspaceName, commandName, selection.Default)
			}
		}
	}
	return nil
}

func (config Config) Resolve(cwd string, commandName string, requestedProfile string) (Resolution, error) {
	canonicalCWD, err := canonicalPath(cwd)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolve working directory: %w", err)
	}

	type match struct {
		name       string
		rootLength int
	}
	var matches []match
	for name, workspace := range config.Workspaces {
		for _, root := range workspace.Roots {
			canonicalRoot, err := canonicalPath(root)
			if err != nil {
				return Resolution{}, fmt.Errorf("resolve workspace %q root %q: %w", name, root, err)
			}
			if pathIsInside(canonicalCWD, canonicalRoot) {
				matches = append(matches, match{name: name, rootLength: len(canonicalRoot)})
			}
		}
	}
	if len(matches) == 0 {
		return Resolution{}, fmt.Errorf("working directory %q is not in a configured workspace", canonicalCWD)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].rootLength > matches[j].rootLength })
	workspaceName := matches[0].name
	workspace := config.Workspaces[workspaceName]
	selection, ok := workspace.Commands[commandName]
	if !ok {
		return Resolution{}, fmt.Errorf("command %q is not allowed in workspace %q", commandName, workspaceName)
	}

	profileName, err := selectProfile(selection, requestedProfile)
	if err != nil {
		return Resolution{}, fmt.Errorf("command %q in workspace %q: %w", commandName, workspaceName, err)
	}
	return Resolution{
		Workspace: workspaceName,
		Command:   commandName,
		Profile:   profileName,
		Launch:    config.Profiles[profileName],
	}, nil
}

func selectProfile(selection CommandSelection, requested string) (string, error) {
	if requested != "" {
		for _, allowed := range selection.Profiles {
			if requested == allowed {
				return requested, nil
			}
		}
		return "", fmt.Errorf("profile %q is not allowed; allowed profiles: %s", requested, strings.Join(selection.Profiles, ", "))
	}
	if selection.Default != "" {
		return selection.Default, nil
	}
	if len(selection.Profiles) == 1 {
		return selection.Profiles[0], nil
	}
	return "", fmt.Errorf("select a profile with --profile; allowed profiles: %s", strings.Join(selection.Profiles, ", "))
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return filepath.Clean(abs), nil
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
