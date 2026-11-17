package gate

import (
	"path/filepath"
	"testing"
)

func TestProfileAllowsWhenOneRootIsInside(t *testing.T) {
	root := t.TempDir()
	profile := Profile{Name: "test", AllowedRoots: []string{root}}

	if !profile.Allows([]string{filepath.Join(root, "project")}) {
		t.Fatal("the profile denied a child path")
	}
	if !profile.Allows([]string{root, filepath.Join(filepath.Dir(root), "other")}) {
		t.Fatal("the profile denied a mixed set with one root inside it")
	}
	if profile.Allows([]string{filepath.Join(filepath.Dir(root), "other")}) {
		t.Fatal("the profile allowed a set with no root inside it")
	}
	if profile.Allows([]string{root + "-lookalike"}) {
		t.Fatal("the profile allowed a path with only a matching prefix")
	}
	if profile.Allows(nil) {
		t.Fatal("the profile allowed an empty identity")
	}
}
