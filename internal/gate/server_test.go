package gate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type fixedRootsHandler struct {
	paths []string
}

type handlerBox struct {
	handler http.Handler
}

type swappingHandler struct {
	current atomic.Value
}

func newSwappingHandler(handler http.Handler) *swappingHandler {
	switcher := &swappingHandler{}
	switcher.current.Store(handlerBox{handler: handler})
	return switcher
}

func (switcher *swappingHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switcher.current.Load().(handlerBox).handler.ServeHTTP(response, request)
}

func (switcher *swappingHandler) Swap(handler http.Handler) {
	switcher.current.Store(handlerBox{handler: handler})
}

func (handler fixedRootsHandler) ListRoots(_ context.Context, _ mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	roots := make([]mcp.Root, 0, len(handler.paths))
	for _, path := range handler.paths {
		roots = append(roots, mcp.Root{URI: (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()})
	}
	return &mcp.ListRootsResult{Roots: roots}, nil
}

func TestRootsClientIsAllowedInsideProfile(t *testing.T) {
	root := t.TempDir()
	result := callProbeWithRoots(t, Profile{Name: "test", AllowedRoots: []string{root}}, []string{filepath.Join(root, "project")})
	if result.IsError {
		t.Fatalf("the gate denied an allowed roots client: %s", toolText(result))
	}
}

func TestRootsClientIsDeniedOutsideProfile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	result := callProbeWithRoots(t, Profile{Name: "test", AllowedRoots: []string{root}}, []string{outside})
	if !result.IsError || !strings.Contains(toolText(result), "denied") {
		t.Fatalf("the gate allowed an outside roots client: %s", toolText(result))
	}
}

func TestCodexMetadataIsAllowedInsideProfile(t *testing.T) {
	root := t.TempDir()
	testServer := httptest.NewServer(NewHandler(Profile{Name: "test", AllowedRoots: []string{root}}))
	defer testServer.Close()

	mcpClient, err := client.NewStreamableHttpClient(testServer.URL + "/mcp")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	startClient(t, mcpClient)
	defer mcpClient.Close()

	result, err := mcpClient.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "jackfield_gate_probe",
		Meta: mcp.NewMetaFromMap(map[string]any{
			codexSandboxMetaKey: map[string]any{"sandboxCwd": "file://" + filepath.Join(root, "project")},
		}),
	}})
	if err != nil {
		t.Fatalf("call probe: %v", err)
	}
	if result.IsError {
		t.Fatalf("the gate denied allowed Codex metadata: %s", toolText(result))
	}
}

func TestInitializeAdvertisesCodexCapability(t *testing.T) {
	root := t.TempDir()
	testServer := httptest.NewServer(NewHandler(Profile{Name: "test", AllowedRoots: []string{root}}))
	defer testServer.Close()

	mcpClient, err := client.NewStreamableHttpClient(testServer.URL + "/mcp")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := mcpClient.Start(context.Background()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer mcpClient.Close()

	result, err := mcpClient.Initialize(context.Background(), mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "jackfield-test", Version: "0.1.0"},
	}})
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	if _, ok := result.Capabilities.Experimental[codexSandboxMetaKey]; !ok {
		t.Fatalf("the initialize response did not advertise the Codex capability: %#v", result.Capabilities)
	}
}

func TestGateRejectsNonLocalHostAndOrigin(t *testing.T) {
	handler := localRequestsOnly(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))

	nonLocalHost := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	nonLocalHost.Host = "example.test"
	hostResponse := httptest.NewRecorder()
	handler.ServeHTTP(hostResponse, nonLocalHost)
	if hostResponse.Code != http.StatusForbidden {
		t.Fatalf("the gate accepted a non-local host: %d", hostResponse.Code)
	}

	nonLocalOrigin := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	nonLocalOrigin.Header.Set("Origin", "https://example.test")
	originResponse := httptest.NewRecorder()
	handler.ServeHTTP(originResponse, nonLocalOrigin)
	if originResponse.Code != http.StatusForbidden {
		t.Fatalf("the gate accepted a non-local origin: %d", originResponse.Code)
	}
}

func TestProxyForwardsAllowedCallsAndBlocksOutsideCalls(t *testing.T) {
	var calls atomic.Int32
	upstreamServer := server.NewMCPServer("mock-slack", "0.1.0", server.WithToolCapabilities(false))
	upstreamServer.AddTool(
		mcp.NewTool("slack_test", mcp.WithDescription("A harmless Slack test tool.")),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return mcp.NewToolResultText("mock Slack reply"), nil
		},
	)
	upstreamHTTP := httptest.NewServer(server.NewStreamableHTTPServer(upstreamServer))
	defer upstreamHTTP.Close()

	upstream, tools, err := ConnectUpstream(context.Background(), upstreamHTTP.URL+"/mcp")
	if err != nil {
		t.Fatalf("connect upstream: %v", err)
	}
	defer upstream.Close()
	if len(tools) != 1 || tools[0].Name != "slack_test" {
		t.Fatalf("the proxy loaded unexpected tools: %#v", tools)
	}

	allowedRoot := t.TempDir()
	proxyHTTP := httptest.NewServer(NewProxyHandler(
		Profile{Name: "smarta", AllowedRoots: []string{allowedRoot}},
		"jackfield-slack-smarta",
		upstream,
		tools,
	))
	defer proxyHTTP.Close()

	allowed := callToolWithCodexPath(t, proxyHTTP.URL+"/mcp", "slack_test", filepath.Join(allowedRoot, "project"))
	if allowed.IsError || toolText(allowed) != "mock Slack reply" {
		t.Fatalf("the proxy did not return the upstream result: %s", toolText(allowed))
	}
	denied := callToolWithCodexPath(t, proxyHTTP.URL+"/mcp", "slack_test", t.TempDir())
	if !denied.IsError {
		t.Fatal("the proxy allowed an outside workspace")
	}
	if calls.Load() != 1 {
		t.Fatalf("the proxy sent %d calls upstream; expected 1", calls.Load())
	}
}

func TestUpstreamReconnectsAfterServerReplacement(t *testing.T) {
	switcher := newSwappingHandler(mockUpstreamHandler("first reply"))
	upstreamHTTP := httptest.NewServer(switcher)
	defer upstreamHTTP.Close()

	upstream, _, err := ConnectUpstream(context.Background(), upstreamHTTP.URL+"/mcp")
	if err != nil {
		t.Fatalf("connect upstream: %v", err)
	}
	defer upstream.Close()

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "slack_test"}}
	first, err := upstream.CallTool(context.Background(), request)
	if err != nil || toolText(first) != "first reply" {
		t.Fatalf("first upstream call failed: result=%v error=%v", first, err)
	}

	switcher.Swap(mockUpstreamHandler("second reply"))
	second, err := upstream.CallTool(context.Background(), request)
	if err != nil || toolText(second) != "second reply" {
		t.Fatalf("reconnected upstream call failed: result=%v error=%v", second, err)
	}
}

func mockUpstreamHandler(reply string) http.Handler {
	mcpServer := server.NewMCPServer("mock-slack", "0.1.0", server.WithToolCapabilities(false))
	mcpServer.AddTool(
		mcp.NewTool("slack_test", mcp.WithDescription("A harmless Slack test tool.")),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(reply), nil
		},
	)
	return server.NewStreamableHTTPServer(mcpServer)
}

func callProbeWithRoots(t *testing.T, profile Profile, paths []string) *mcp.CallToolResult {
	t.Helper()
	testServer := httptest.NewServer(NewHandler(profile))
	defer testServer.Close()

	httpTransport, err := transport.NewStreamableHTTP(
		testServer.URL+"/mcp",
		transport.WithContinuousListening(),
	)
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	mcpClient := client.NewClient(httpTransport, client.WithRootsHandler(fixedRootsHandler{paths: paths}))
	startClient(t, mcpClient)
	defer mcpClient.Close()

	result, err := mcpClient.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "jackfield_gate_probe",
	}})
	if err != nil {
		t.Fatalf("call probe: %v", err)
	}
	return result
}

func startClient(t *testing.T, mcpClient *client.Client) {
	t.Helper()
	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	_, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "jackfield-test", Version: "0.1.0"},
	}})
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
}

func callToolWithCodexPath(t *testing.T, endpoint string, toolName string, path string) *mcp.CallToolResult {
	t.Helper()
	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	startClient(t, mcpClient)
	defer mcpClient.Close()

	result, err := mcpClient.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: toolName,
		Meta: mcp.NewMetaFromMap(map[string]any{
			codexSandboxMetaKey: map[string]any{"sandboxCwd": (&url.URL{Scheme: "file", Path: path}).String()},
		}),
	}})
	if err != nil {
		t.Fatalf("call %s: %v", toolName, err)
	}
	return result
}

func toolText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if item, ok := content.(mcp.TextContent); ok {
			fmt.Fprint(&text, item.Text)
		}
	}
	return text.String()
}
