package runner

import (
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
					"gog": {Profiles: []string{"gog-work"}, Default: "gog-work"},
					"aws": {Profiles: []string{"aws-stage", "aws-production"}},
				},
			},
		},
		Profiles: map[string]Profile{
			"gog-work":       {Executable: "/bin/gog"},
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
