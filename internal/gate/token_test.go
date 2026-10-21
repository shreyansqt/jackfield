package gate

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const testToken = "0123456789abcdef0123456789abcdef-test"

func callProbeWithToken(t *testing.T, profile Profile, headers map[string]string) *mcp.CallToolResult {
	t.Helper()
	testServer := httptest.NewServer(NewHandler(profile))
	defer testServer.Close()

	mcpClient, err := client.NewStreamableHttpClient(testServer.URL+"/mcp", transport.WithHTTPHeaders(headers))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	startClient(t, mcpClient)
	defer mcpClient.Close()

	result, err := mcpClient.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "jackfield_gate_probe"}})
	if err != nil {
		t.Fatalf("call probe: %v", err)
	}
	return result
}

func tokenProfile(t *testing.T) Profile {
	t.Helper()
	return Profile{Name: "test", AllowedRoots: []string{t.TempDir()}, token: []byte(testToken)}
}

func TestValidTokenIsAllowed(t *testing.T) {
	result := callProbeWithToken(t, tokenProfile(t), map[string]string{TokenHeader: testToken})
	if result.IsError {
		t.Fatalf("the gate denied a valid token: %s", toolText(result))
	}
}

func TestWrongTokenIsDenied(t *testing.T) {
	result := callProbeWithToken(t, tokenProfile(t), map[string]string{TokenHeader: testToken + "x"})
	if !result.IsError || !strings.Contains(toolText(result), "not valid") {
		t.Fatalf("the gate allowed a wrong token: %s", toolText(result))
	}
}

func TestEmptyTokenIsDenied(t *testing.T) {
	result := callProbeWithToken(t, tokenProfile(t), map[string]string{TokenHeader: ""})
	if !result.IsError {
		t.Fatalf("the gate allowed an empty token: %s", toolText(result))
	}
}

func TestTokenIsDeniedWhenProfileHasNoToken(t *testing.T) {
	profile := Profile{Name: "test", AllowedRoots: []string{t.TempDir()}}
	result := callProbeWithToken(t, profile, map[string]string{TokenHeader: testToken})
	if !result.IsError || !strings.Contains(toolText(result), "does not accept workspace tokens") {
		t.Fatalf("the gate allowed a token for a profile without one: %s", toolText(result))
	}
}

func TestNoTokenKeepsTheRootsRule(t *testing.T) {
	result := callProbeWithToken(t, tokenProfile(t), nil)
	if !result.IsError || !strings.Contains(toolText(result), "no workspace identity") {
		t.Fatalf("a client without token or roots was not denied for lack of identity: %s", toolText(result))
	}
}

func TestLoadPolicyReadsTokenFile(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "gate.json")
	config := `{"profiles":{"p":{"allowed_roots":["` + directory + `"],"token_file":"` + tokenPath + `"}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := LoadPolicy(configPath, "p")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if string(profile.token) != testToken {
		t.Fatalf("the token was not trimmed and loaded: %q", profile.token)
	}
}

func TestLoadPolicyRejectsBadTokenFiles(t *testing.T) {
	directory := t.TempDir()
	cases := map[string]struct {
		contents string
		mode     os.FileMode
	}{
		"too short":       {contents: "short", mode: 0o600},
		"group readable":  {contents: testToken, mode: 0o640},
		"world readable":  {contents: testToken, mode: 0o644},
		"whitespace only": {contents: strings.Repeat(" ", 64), mode: 0o600},
		"too long":        {contents: strings.Repeat("a", maxTokenLength+1), mode: 0o600},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			tokenPath := filepath.Join(directory, strings.ReplaceAll(name, " ", "-"))
			if err := os.WriteFile(tokenPath, []byte(testCase.contents), testCase.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(tokenPath, testCase.mode); err != nil {
				t.Fatal(err)
			}
			configPath := tokenPath + ".json"
			config := `{"profiles":{"p":{"allowed_roots":["` + directory + `"],"token_file":"` + tokenPath + `"}}}`
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPolicy(configPath, "p"); err == nil {
				t.Fatal("the policy accepted a bad token file")
			}
		})
	}
}

func TestLoadPolicyFailsClosedWhenTokenFileIsMissing(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "gate.json")
	config := `{"profiles":{"p":{"allowed_roots":["` + directory + `"],"token_file":"` + filepath.Join(directory, "absent") + `"}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(configPath, "p"); err == nil {
		t.Fatal("the policy loaded although its token file is missing")
	}
}
