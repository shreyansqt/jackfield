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
	if err == nil || !strings.Contains(err.Error(), "not in a configured workspace") {
		t.Fatalf("expected workspace error, got %v", err)
	}

	root := t.TempDir()
	_, err = testConfig(root).Resolve(root, "aws", "")
	if err == nil || !strings.Contains(err.Error(), "select a profile") {
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
	if _, _, err := testConfig(root).ResolveArgs(root, "aws", "", []string{"sts"}); err == nil || !strings.Contains(err.Error(), "select a profile") {
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
		Interactive: []Interactive{
			{
				Subcommand:     []string{"auth", "add"},
				DropPrefixArgs: []string{"--no-input"},
				Identity:       "pinned@example.com",
			},
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
	rules := config.Profiles["gog-work"].Interactive
	if len(rules) != 1 || rules[0].Identity != "pinned@example.com" {
		t.Fatalf("unexpected interactive rules: %+v", rules)
	}
}

func TestValidateRejectsInteractiveRuleWithoutSubcommand(t *testing.T) {
	config := testConfig(t.TempDir())
	config.Profiles["gog-work"] = Profile{
		Executable:  "/bin/gog",
		Interactive: []Interactive{{Identity: "pinned@example.com"}},
	}
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "without a subcommand") {
		t.Fatalf("expected a missing subcommand error, got %v", err)
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
