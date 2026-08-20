package gate

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const codexSandboxMetaKey = "codex/sandbox-state-meta"

type Identity struct {
	Source string
	Paths  []string
}

func identityFromCodexMeta(meta *mcp.Meta) (Identity, bool, error) {
	if meta == nil {
		return Identity{}, false, nil
	}
	raw, ok := meta.AdditionalFields[codexSandboxMetaKey]
	if !ok {
		return Identity{}, false, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return Identity{}, true, fmt.Errorf("Codex sandbox metadata is not an object")
	}
	cwd, ok := fields["sandboxCwd"].(string)
	if !ok {
		cwd, ok = fields["sandbox_cwd"].(string)
	}
	if !ok || cwd == "" {
		return Identity{}, true, fmt.Errorf("Codex sandbox metadata has no sandboxCwd")
	}
	path, err := pathFromFileURIOrAbsolutePath(cwd)
	if err != nil {
		return Identity{}, true, fmt.Errorf("Codex sandbox path is invalid: %w", err)
	}
	return Identity{Source: "codex-sandbox", Paths: []string{path}}, true, nil
}

func pathFromFileURIOrAbsolutePath(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "file" || (parsed.Host != "" && parsed.Host != "localhost") {
			return "", fmt.Errorf("%q is not a local file URI", value)
		}
		return canonicalPath(parsed.Path)
	}
	return canonicalPath(value)
}

func identityFromRoots(ctx context.Context) (Identity, error) {
	session := server.ClientSessionFromContext(ctx)
	clientInfo, ok := session.(server.SessionWithClientInfo)
	if !ok || clientInfo.GetClientCapabilities().Roots == nil {
		return Identity{}, fmt.Errorf("the client supplied no workspace identity")
	}

	rootsSession, ok := session.(server.SessionWithRoots)
	if !ok {
		return Identity{}, fmt.Errorf("the client cannot answer a roots request")
	}

	requestContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := rootsSession.ListRoots(requestContext, mcp.ListRootsRequest{})
	if err != nil {
		return Identity{}, fmt.Errorf("the roots request failed: %w", err)
	}
	if len(result.Roots) == 0 {
		return Identity{}, fmt.Errorf("the client returned no workspace roots")
	}

	paths := make([]string, 0, len(result.Roots))
	for _, root := range result.Roots {
		path, err := pathFromFileURIOrAbsolutePath(root.URI)
		if err != nil {
			return Identity{}, fmt.Errorf("root %q is invalid: %w", root.URI, err)
		}
		paths = append(paths, path)
	}
	return Identity{Source: "mcp-roots", Paths: paths}, nil
}

func discoverIdentity(ctx context.Context, request mcp.CallToolRequest) (Identity, error) {
	if identity, found, err := identityFromCodexMeta(request.Params.Meta); found {
		return identity, err
	}
	return identityFromRoots(ctx)
}
