package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testConfig(root string) Config {
	return Config{
		Version: 1,
		Workspaces: map[string]Workspace{
			"work": {
				Roots: []string{root},
				Commands: map[string]CommandSelection{
					"gog":      {Profiles: []string{"gog-work"}, Default: "gog-work"},
					"wrangler": {Profiles: []string{"wrangler-work"}, Default: "wrangler-work"},
					"aws": {
						Profiles: []string{"aws-stage", "aws-production"},
						Aliases: map[string]string{
							"upstream-stage":      "aws-stage",
							"upstream-production": "aws-production",
						},
					},
				},
			},
		},
		Profiles: map[string]Profile{
			"gog-work":       {Executable: "/bin/gog"},
			"wrangler-work":  {Executable: "/bin/wrangler"},
			"aws-stage":      {Executable: "/bin/aws"},
			"aws-production": {Executable: "/bin/aws"},
		},
	}
}

func TestResolveSelectsWorkspaceDefault(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	resolution, err := testConfig(root).Resolve(child, "gog", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Workspace != "work" || resolution.Profile != "gog-work" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestResolveUsesCanonicalWorkspacePath(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-work")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	resolution, err := testConfig(root).Resolve(link, "gog", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Workspace != "work" {
		t.Fatalf("unexpected workspace: %s", resolution.Workspace)
	}
}

func TestResolveRequiresChoiceForMultipleProfiles(t *testing.T) {
	_, err := testConfig(t.TempDir()).Resolve(t.TempDir(), "aws", "")
	if err == nil || !strings.Contains(err.Error(), "is in no workspace of this manifest") {
		t.Fatalf("expected workspace error, got %v", err)
	}

	root := t.TempDir()
	_, err = testConfig(root).Resolve(root, "aws", "")
	if err == nil || !strings.Contains(err.Error(), "Name one with --profile") {
		t.Fatalf("expected profile choice error, got %v", err)
	}
}

func TestResolveAcceptsOnlyAllowedRequestedProfile(t *testing.T) {
	root := t.TempDir()
	resolution, err := testConfig(root).Resolve(root, "aws", "aws-stage")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Profile != "aws-stage" {
		t.Fatalf("unexpected profile: %s", resolution.Profile)
	}

	_, err = testConfig(root).Resolve(root, "aws", "gog-work")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected denied profile, got %v", err)
	}
}

func TestResolveArgsSelectsAliasAndStripsIdentityFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--profile", "upstream-production", "ecr", "describe-images"},
		{"--profile=upstream-production", "ecr", "describe-images"},
	} {
		root := t.TempDir()
		resolution, childArgs, err := testConfig(root).ResolveArgs(root, "aws", "", args)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Profile != "aws-production" {
			t.Fatalf("unexpected profile: %s", resolution.Profile)
		}
		want := []string{"ecr", "describe-images"}
		if !reflect.DeepEqual(childArgs, want) {
			t.Fatalf("got arguments %v, want %v", childArgs, want)
		}
		if err := ValidateArgs(childArgs, []string{"--profile"}); err != nil {
			t.Fatalf("sanitized arguments failed validation: %v", err)
		}
	}
}

func TestResolveArgsRejectsUnknownAlias(t *testing.T) {
	root := t.TempDir()
	_, _, err := testConfig(root).ResolveArgs(root, "aws", "", []string{"--profile", "unknown", "sts"})
	if err == nil || !strings.Contains(err.Error(), "profile alias \"unknown\" is not configured") {
		t.Fatalf("expected unknown alias error, got %v", err)
	}
}

func TestResolveArgsAcceptsJackfieldProfileName(t *testing.T) {
	root := t.TempDir()
	resolution, childArgs, err := testConfig(root).ResolveArgs(root, "aws", "", []string{"sts", "--profile=aws-stage"})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Profile != "aws-stage" || !reflect.DeepEqual(childArgs, []string{"sts"}) {
		t.Fatalf("unexpected resolution or arguments: %+v %v", resolution, childArgs)
	}
}

func TestResolveArgsKeepsNoFlagBehavior(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"gog", "wrangler"} {
		resolution, _, err := testConfig(root).ResolveArgs(root, command, "", []string{"version"})
		if err != nil {
			t.Fatalf("%s did not use its default: %v", command, err)
		}
		if resolution.Profile != command+"-work" {
			t.Fatalf("%s selected profile %q", command, resolution.Profile)
		}
	}
	if _, _, err := testConfig(root).ResolveArgs(root, "aws", "", []string{"sts"}); err == nil || !strings.Contains(err.Error(), "Name one with --profile") {
		t.Fatalf("expected AWS profile choice error, got %v", err)
	}
}

func TestLoadAcceptsProfileAliases(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := fmt.Sprintf(`version: 1
workspaces:
  work:
    roots: [%q]
    commands:
      tool:
        profiles: [tool-work]
        aliases:
          upstream-work: tool-work
profiles:
  tool-work:
    executable: /bin/tool
`, root)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if actual := config.Workspaces["work"].Commands["tool"].Aliases["upstream-work"]; actual != "tool-work" {
		t.Fatalf("unexpected alias target: %q", actual)
	}
}

func TestValidateRejectsDisallowedAliasTarget(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	selection := config.Workspaces["work"].Commands["aws"]
	selection.Aliases["upstream-other"] = "gog-work"
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "uses disallowed profile") {
		t.Fatalf("expected disallowed alias target error, got %v", err)
	}
}

func TestBuildEnvClearsAmbientCredentials(t *testing.T) {
	actual := BuildEnv(
		[]string{"PATH=/bin", "AWS_PROFILE=wrong", "AWS_ACCESS_KEY_ID=secret"},
		[]string{"AWS_ACCESS_KEY_ID"},
		map[string]string{"AWS_PROFILE": "safe"},
	)
	want := []string{"PATH=/bin", "AWS_PROFILE=safe"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func gogProfile() Profile {
	return Profile{
		Executable: "/bin/gog",
		PrefixArgs: []string{"-a", "pinned@example.com", "--no-input"},
		DeniedArgs: []string{"-a", "--account", "--access-token", "--client", "--home"},
		SubcommandOverrides: []SubcommandOverride{
			{
				Subcommand:     []string{"auth", "add"},
				DropPrefixArgs: []string{"--no-input"},
				Identity:       "pinned@example.com",
			},
		},
	}
}

// wranglerProfile mirrors the real wrangler profiles. The identity is a valued
// flag in the prefix, which is what separates this case from the gog one.
func wranglerProfile() Profile {
	return Profile{
		Executable: "/bin/mise",
		PrefixArgs: []string{"exec", "node@23", "--", "wrangler", "--profile", "default"},
		DeniedArgs: []string{"--profile"},
		SubcommandOverrides: []SubcommandOverride{
			{Subcommand: []string{"whoami"}, DropPrefixArgs: []string{"--profile"}},
			{Subcommand: []string{"login"}, DropPrefixArgs: []string{"--profile"}},
			{Subcommand: []string{"logout"}, DropPrefixArgs: []string{"--profile"}},
			{Subcommand: []string{"auth"}, DropPrefixArgs: []string{"--profile"}},
		},
	}
}

func TestLaunchPrefixAllowsInteractiveAuthForPinnedIdentity(t *testing.T) {
	actual, err := gogProfile().launchPrefix([]string{"auth", "add", "pinned@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-a", "pinned@example.com"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestLaunchPrefixDeniesInteractiveAuthForOtherIdentity(t *testing.T) {
	_, err := gogProfile().launchPrefix([]string{"auth", "add", "someone@example.com"})
	if err == nil || !strings.Contains(err.Error(), "pinned to \"pinned@example.com\"") {
		t.Fatalf("expected a pinned identity error, got %v", err)
	}
}

func TestLaunchPrefixKeepsNoInputForOtherCommands(t *testing.T) {
	profile := gogProfile()
	for _, args := range [][]string{
		{"whoami", "--plain"},
		{"auth", "list"},
		{"auth", "remove", "someone@example.com"},
		{"drive", "search", "auth", "add"},
	} {
		actual, err := profile.launchPrefix(args)
		if err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if !reflect.DeepEqual(actual, profile.PrefixArgs) {
			t.Fatalf("%v got prefix %v, want %v", args, actual, profile.PrefixArgs)
		}
	}
}

func TestLaunchPrefixKeepsDeniedArgsForInteractiveAuth(t *testing.T) {
	_, err := gogProfile().launchPrefix([]string{"auth", "add", "--account", "someone@example.com"})
	if err == nil || !strings.Contains(err.Error(), "override the selected identity") {
		t.Fatalf("expected a denied argument error, got %v", err)
	}
}

func TestLaunchPrefixMatchesSubcommandAfterFlags(t *testing.T) {
	actual, err := gogProfile().launchPrefix([]string{"--verbose", "auth", "add", "pinned@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, []string{"-a", "pinned@example.com"}) {
		t.Fatalf("unexpected prefix: %v", actual)
	}
}

// A wrangler account command must lose the identity flag, and it must lose the
// flag's value with it. A bare `default` left in the prefix becomes wrangler's
// subcommand, and the command fails a second way.
func TestLaunchPrefixDropsValuedIdentityFlagForAccountCommands(t *testing.T) {
	profile := wranglerProfile()
	want := []string{"exec", "node@23", "--", "wrangler"}
	for _, args := range [][]string{
		{"whoami"},
		{"login"},
		{"logout"},
		{"auth", "status"},
		{"auth", "create"},
		{"auth", "keyring", "enable"},
		{"--experimental-json-config", "whoami"},
	} {
		actual, err := profile.launchPrefix(args)
		if err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("%v got prefix %v, want %v", args, actual, want)
		}
	}
}

func TestLaunchPrefixKeepsIdentityFlagForResourceCommands(t *testing.T) {
	profile := wranglerProfile()
	for _, args := range [][]string{
		{"r2", "bucket", "list"},
		{"deploy"},
		{"secret", "put", "TOKEN"},
		{"d1", "execute", "db", "--command", "select 1"},
		// The rule matches the first subcommand word only. A resource command
		// that merely mentions `whoami` later keeps its identity.
		{"kv", "key", "get", "whoami"},
	} {
		actual, err := profile.launchPrefix(args)
		if err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if !reflect.DeepEqual(actual, profile.PrefixArgs) {
			t.Fatalf("%v got prefix %v, want %v", args, actual, profile.PrefixArgs)
		}
	}
}

// Suppressing the prefix flag must not let the child supply its own. Otherwise
// the rule that unblocks `wrangler whoami` also opens a hole in every command it
// matches.
func TestLaunchPrefixKeepsDeniedArgsForAccountCommands(t *testing.T) {
	profile := wranglerProfile()
	for _, args := range [][]string{
		{"whoami", "--profile", "other"},
		{"login", "--profile=other"},
		{"auth", "status", "--profile", "other"},
	} {
		if _, err := profile.launchPrefix(args); err == nil || !strings.Contains(err.Error(), "override the selected identity") {
			t.Fatalf("expected %v to be denied, got %v", args, err)
		}
	}
}

// The gog profiles have no valued flag to drop. This guards the shared
// removeArgs change against breaking the rule it was written for.
func TestLaunchPrefixLeavesGogProfilesUnchanged(t *testing.T) {
	actual, err := gogProfile().launchPrefix([]string{"auth", "add", "pinned@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-a", "pinned@example.com"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestRemoveArgsDropsJoinedFlagValue(t *testing.T) {
	actual := removeArgs([]string{"wrangler", "--profile=default", "--no-input"}, []string{"--profile"})
	want := []string{"wrangler", "--no-input"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestLoadAcceptsInteractiveRules(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := fmt.Sprintf(`version: 1
workspaces:
  work:
    roots: [%q]
    commands:
      gog:
        profiles: [gog-work]
profiles:
  gog-work:
    executable: /bin/gog
    prefix_args: [-a, pinned@example.com, --no-input]
    interactive:
      - subcommand: [auth, add]
        drop_prefix_args: [--no-input]
        identity: pinned@example.com
`, root)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rules := config.Profiles["gog-work"].Overrides()
	if len(rules) != 1 || rules[0].Identity != "pinned@example.com" {
		t.Fatalf("unexpected subcommand overrides: %+v", rules)
	}
}

// subcommand_overrides is the documented key. interactive is the name it had
// first, and a machine may still run an older manifest, so both must parse.
func TestLoadAcceptsSubcommandOverrides(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := fmt.Sprintf(`version: 1
workspaces:
  work:
    roots: [%q]
    commands:
      wrangler:
        profiles: [wrangler-work]
profiles:
  wrangler-work:
    executable: /bin/wrangler
    prefix_args: [--profile, default]
    denied_args: [--profile]
    subcommand_overrides:
      - subcommand: [whoami]
        drop_prefix_args: [--profile]
`, root)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rules := config.Profiles["wrangler-work"].Overrides()
	if len(rules) != 1 || !reflect.DeepEqual(rules[0].Subcommand, []string{"whoami"}) {
		t.Fatalf("unexpected subcommand overrides: %+v", rules)
	}
}

func TestValidateRejectsOverrideWithoutSubcommand(t *testing.T) {
	config := testConfig(t.TempDir())
	config.Profiles["gog-work"] = Profile{
		Executable:          "/bin/gog",
		SubcommandOverrides: []SubcommandOverride{{Identity: "pinned@example.com"}},
	}
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "without a subcommand") {
		t.Fatalf("expected a missing subcommand error, got %v", err)
	}
}

// A rule that drops an argument the prefix never had does nothing, and the
// command it was written for still fails. That is a typo, so the manifest must
// not load.
func TestValidateRejectsOverrideThatDropsAnAbsentPrefixArg(t *testing.T) {
	config := testConfig(t.TempDir())
	config.Profiles["wrangler-work"] = Profile{
		Executable: "/bin/wrangler",
		PrefixArgs: []string{"--profile", "default"},
		SubcommandOverrides: []SubcommandOverride{
			{Subcommand: []string{"whoami"}, DropPrefixArgs: []string{"--prof1le"}},
		},
	}
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "not in its prefix_args") {
		t.Fatalf("expected an absent prefix argument error, got %v", err)
	}
}

func TestValidateArgsBlocksIdentityOverride(t *testing.T) {
	for _, args := range [][]string{
		{"--profile", "other"},
		{"--profile=other"},
		{"--account", "other@example.com"},
		{"-aother@example.com"},
	} {
		if err := ValidateArgs(args, []string{"--profile", "--account", "-a"}); err == nil {
			t.Fatalf("expected %v to fail", args)
		}
	}
	if err := ValidateArgs([]string{"r2", "bucket", "list"}, []string{"--profile"}); err != nil {
		t.Fatalf("expected normal arguments to pass: %v", err)
	}
}

// The manifest carries the hub address for internal/hub. This decoder sets
// KnownFields(true), so a hub key it does not know would fail the parse and stop
// every `jf run` command on a machine that uses the hub.
func TestLoadAcceptsTheHubAddress(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := fmt.Sprintf(`version: 1
hub: https://hub.example.com
workspaces:
  work:
    roots: [%q]
    commands:
      tool:
        profiles: [tool-work]
profiles:
  tool-work:
    executable: /bin/tool
`, root)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("a manifest with a hub key must still parse: %v", err)
	}
	if config.Hub != "https://hub.example.com" {
		t.Fatalf("got hub %q, want https://hub.example.com", config.Hub)
	}

	// A manifest without the key stays valid, because a machine may use the
	// gate without any hub.
	if _, err := Load(writeManifestWithoutHub(t, root)); err != nil {
		t.Fatalf("a manifest with no hub key must still parse: %v", err)
	}
}

func writeManifestWithoutHub(t *testing.T, root string) string {
	t.Helper()
	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := fmt.Sprintf(`version: 1
workspaces:
  work:
    roots: [%q]
    commands:
      tool:
        profiles: [tool-work]
profiles:
  tool-work:
    executable: /bin/tool
`, root)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}
