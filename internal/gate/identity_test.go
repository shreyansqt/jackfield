package gate

import (
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestIdentityFromCodexMeta(t *testing.T) {
	root := t.TempDir()
	requestMeta := mcp.NewMetaFromMap(map[string]any{
		codexSandboxMetaKey: map[string]any{
			"sandboxCwd": "file://" + filepath.Join(root, "project"),
		},
	})

	identity, found, err := identityFromCodexMeta(requestMeta)
	if err != nil {
		t.Fatalf("identityFromCodexMeta returned an error: %v", err)
	}
	if !found {
		t.Fatal("identityFromCodexMeta did not find the metadata")
	}
	if identity.Source != "codex-sandbox" || len(identity.Paths) != 1 {
		t.Fatalf("identityFromCodexMeta returned an unexpected identity: %#v", identity)
	}
}

func TestIdentityFromCodexMetaRejectsMissingPath(t *testing.T) {
	requestMeta := mcp.NewMetaFromMap(map[string]any{
		codexSandboxMetaKey: map[string]any{},
	})

	_, found, err := identityFromCodexMeta(requestMeta)
	if !found || err == nil {
		t.Fatal("identityFromCodexMeta accepted incomplete metadata")
	}
}
