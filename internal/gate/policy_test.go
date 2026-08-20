package gate

import (
	"path/filepath"
	"testing"
)

func TestProfileAllowsOnlyPathsInsideItsRoots(t *testing.T) {
	root := t.TempDir()
	profile := Profile{Name: "test", AllowedRoots: []string{root}}

	if !profile.Allows([]string{filepath.Join(root, "project")}) {
		t.Fatal("the profile denied a child path")
	}
	if profile.Allows([]string{root, filepath.Join(filepath.Dir(root), "other")}) {
		t.Fatal("the profile allowed a mixed set of roots")
	}
	if profile.Allows([]string{root + "-lookalike"}) {
		t.Fatal("the profile allowed a path with only a matching prefix")
	}
	if profile.Allows(nil) {
		t.Fatal("the profile allowed an empty identity")
	}
}
