package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// agentEnv reads the JF_AGENT environment variable. It is a package variable so
// that a test injects a value without touching the process environment, in the
// same style as the color check that passes os.Getenv as a function.
var agentEnv = func() string {
	return strings.TrimSpace(os.Getenv("JF_AGENT"))
}

type Config struct {
	Version    int                  `yaml:"version"`
	Workspaces map[string]Workspace `yaml:"workspaces"`
	Profiles   map[string]Profile   `yaml:"profiles"`

	// Hub is the address of the jackfield hub, for example
	// "https://hub.example.com". The runner itself does not use it; internal/hub
	// reads it. The field exists here because this decoder sets
	// KnownFields(true), so a manifest with a hub key would otherwise fail to
	// parse and stop every `jf run` command.
	Hub string `yaml:"hub,omitempty"`
}

type Workspace struct {
	Roots    []string                    `yaml:"roots"`
	Commands map[string]CommandSelection `yaml:"commands"`

	// Agent names an agent identity that this workspace claims. When the
	// JF_AGENT environment variable equals this value, the workspace is
	// selected regardless of the working directory. A harness whose agents
	// share one directory (Hermes on the Mac mini) cannot be told apart by
	// path; the declared agent is the second scoping dimension for that case.
	// A workspace may have roots, an agent, or both.
	Agent string `yaml:"agent,omitempty"`
}

type CommandSelection struct {
	Profiles []string          `yaml:"profiles"`
	Default  string            `yaml:"default,omitempty"`
	Aliases  map[string]string `yaml:"aliases,omitempty"`
}

type Profile struct {
	Executable string            `yaml:"executable"`
	PrefixArgs []string          `yaml:"prefix_args,omitempty"`
	DeniedArgs []string          `yaml:"denied_args,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	UnsetEnv   []string          `yaml:"unset_env,omitempty"`

	// SubcommandOverrides holds the rules that change the prefix for some
	// subcommands. Interactive is the older name for the same list. Read them
	// with Overrides rather than either field, because a profile may use either
	// manifest key.
	SubcommandOverrides []SubcommandOverride `yaml:"subcommand_overrides,omitempty"`
	Interactive         []SubcommandOverride `yaml:"interactive,omitempty"`
}

// SubcommandOverride describes one subcommand that the profile prefix breaks. The
// gate drops DropPrefixArgs from the profile prefix for that subcommand, and it
// requires that every identity argument equals Identity.
//
// Two kinds of command need this. A browser login must reach a terminal, so it
// drops the flag that suppresses prompts, such as gog's --no-input. An account
// command rejects the identity flag itself, so it drops that flag: `wrangler
// whoami` fails when it is given --profile.
type SubcommandOverride struct {
	Subcommand     []string `yaml:"subcommand"`
	DropPrefixArgs []string `yaml:"drop_prefix_args,omitempty"`
	Identity       string   `yaml:"identity,omitempty"`
}

// Overrides returns the profile's subcommand rules from whichever manifest key
// carries them. A profile that sets both keys gets both lists, in manifest order.
func (profile Profile) Overrides() []SubcommandOverride {
	if len(profile.Interactive) == 0 {
		return profile.SubcommandOverrides
	}
	if len(profile.SubcommandOverrides) == 0 {
		return profile.Interactive
	}
	merged := make([]SubcommandOverride, 0, len(profile.SubcommandOverrides)+len(profile.Interactive))
	merged = append(merged, profile.SubcommandOverrides...)
	return append(merged, profile.Interactive...)
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
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("no manifest at %s. Write one there, or name a different file with --config PATH or the JF_CONFIG environment variable", path)
		}
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
		for _, rule := range profile.Overrides() {
			if len(rule.Subcommand) == 0 {
				return fmt.Errorf("profile %q has a subcommand override without a subcommand", name)
			}
			for _, word := range rule.Subcommand {
				if strings.TrimSpace(word) == "" || strings.HasPrefix(word, "-") {
					return fmt.Errorf("profile %q subcommand override %q needs plain subcommand words", name, strings.Join(rule.Subcommand, " "))
				}
			}
			// A dropped argument that the prefix does not contain is a typo. It
			// does nothing, and the command it was meant to unblock still fails.
			for _, dropped := range rule.DropPrefixArgs {
				if !containsArg(profile.PrefixArgs, dropped) {
					return fmt.Errorf("profile %q subcommand override %q drops %q, which is not in its prefix_args", name, strings.Join(rule.Subcommand, " "), dropped)
				}
			}
		}
	}
	for workspaceName, workspace := range config.Workspaces {
		if len(workspace.Roots) == 0 && strings.TrimSpace(workspace.Agent) == "" {
			return fmt.Errorf("workspace %q has no roots and no agent; give it at least one", workspaceName)
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
			for alias, profileName := range selection.Aliases {
				if strings.TrimSpace(alias) == "" {
					return fmt.Errorf("workspace %q command %q has an empty profile alias", workspaceName, commandName)
				}
				if !allowed[profileName] {
					return fmt.Errorf("workspace %q command %q alias %q uses disallowed profile %q", workspaceName, commandName, alias, profileName)
				}
				if allowed[alias] && alias != profileName {
					return fmt.Errorf("workspace %q command %q alias %q conflicts with an allowed profile", workspaceName, commandName, alias)
				}
			}
		}
	}
	return nil
}

func (config Config) Resolve(cwd string, commandName string, requestedProfile string) (Resolution, error) {
	resolution, _, err := config.ResolveArgs(cwd, commandName, requestedProfile, nil)
	return resolution, err
}

// resolveWorkspace picks the workspace for this call. It has two dimensions.
//
// The first is the declared agent. When JF_AGENT is set and equals some
// workspace's agent:, that workspace is selected. An explicit agent match wins
// over a directory-root match, because it is the more specific claim: the
// harness states which agent it is, rather than the gate guessing from a path.
//
// The second is the working directory, the original behavior. It applies when
// JF_AGENT is unset, or set to a value that no workspace agent equals. A set-but-
// unmatched JF_AGENT falls back to directory resolution rather than failing,
// because a machine may set JF_AGENT for one tool and still run others normally.
func (config Config) resolveWorkspace(cwd string) (string, error) {
	if agent := agentEnv(); agent != "" {
		for name, workspace := range config.Workspaces {
			if workspace.Agent == agent {
				return name, nil
			}
		}
	}
	return config.resolveWorkspaceByDirectory(cwd)
}

func (config Config) resolveWorkspaceByDirectory(cwd string) (string, error) {
	canonicalCWD, err := canonicalPath(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
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
				return "", fmt.Errorf("resolve workspace %q root %q: %w", name, root, err)
			}
			if pathIsInside(canonicalCWD, canonicalRoot) {
				matches = append(matches, match{name: name, rootLength: len(canonicalRoot)})
			}
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("the working directory %q is in no workspace of this manifest. Add a workspace whose roots: cover this directory, or change to a directory that a workspace already covers", canonicalCWD)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].rootLength > matches[j].rootLength })
	return matches[0].name, nil
}

func (config Config) ResolveArgs(cwd string, commandName string, requestedProfile string, args []string) (Resolution, []string, error) {
	workspaceName, err := config.resolveWorkspace(cwd)
	if err != nil {
		return Resolution{}, nil, err
	}
	workspace := config.Workspaces[workspaceName]
	selection, ok := workspace.Commands[commandName]
	if !ok {
		return Resolution{}, nil, fmt.Errorf("the workspace %q does not allow the command %q. Add it under that workspace's commands: in the manifest, or run the command in a workspace that allows it", workspaceName, commandName)
	}

	requestedProfile, childArgs, err := selectProfileArgs(selection, requestedProfile, args)
	if err != nil {
		return Resolution{}, nil, fmt.Errorf("command %q in workspace %q: %w", commandName, workspaceName, err)
	}
	profileName, err := selectProfile(selection, requestedProfile)
	if err != nil {
		return Resolution{}, nil, fmt.Errorf("command %q in workspace %q: %w", commandName, workspaceName, err)
	}
	return Resolution{
		Workspace: workspaceName,
		Command:   commandName,
		Profile:   profileName,
		Launch:    config.Profiles[profileName],
	}, childArgs, nil
}

func selectProfile(selection CommandSelection, requested string) (string, error) {
	if requested != "" {
		for _, allowed := range selection.Profiles {
			if requested == allowed {
				return requested, nil
			}
		}
		if profileName, ok := selection.Aliases[requested]; ok {
			return profileName, nil
		}
		return "", fmt.Errorf("the profile %q is not allowed here. The allowed profiles are: %s", requested, strings.Join(selection.Profiles, ", "))
	}
	if selection.Default != "" {
		return selection.Default, nil
	}
	if len(selection.Profiles) == 1 {
		return selection.Profiles[0], nil
	}
	return "", fmt.Errorf("this command has more than one allowed profile and no default. Name one with --profile. The allowed profiles are: %s", strings.Join(selection.Profiles, ", "))
}

func selectProfileArgs(selection CommandSelection, requested string, args []string) (string, []string, error) {
	if len(selection.Aliases) == 0 {
		return requested, args, nil
	}

	selected, err := selectRequestedProfile(selection, requested)
	if err != nil {
		return "", nil, err
	}
	cleaned := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := ""
		switch {
		case arg == "--profile":
			if index+1 >= len(args) || args[index+1] == "" {
				return "", nil, fmt.Errorf("--profile needs a profile name")
			}
			index++
			value = args[index]
		case strings.HasPrefix(arg, "--profile="):
			value = strings.TrimPrefix(arg, "--profile=")
			if value == "" {
				return "", nil, fmt.Errorf("--profile needs a profile name")
			}
		default:
			cleaned = append(cleaned, arg)
			continue
		}

		profileName, err := selectProfile(selection, value)
		if err != nil {
			return "", nil, fmt.Errorf("profile alias %q is not configured", value)
		}
		if selected != "" && selected != profileName {
			return "", nil, fmt.Errorf("conflicting profiles %q and %q", selected, profileName)
		}
		selected = profileName
	}
	return selected, cleaned, nil
}

func selectRequestedProfile(selection CommandSelection, requested string) (string, error) {
	if requested == "" {
		return "", nil
	}
	return selectProfile(selection, requested)
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
