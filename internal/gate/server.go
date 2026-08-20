package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const DefaultReadHeaderTimeout = 5 * time.Second

func NewHandler(profile Profile) http.Handler {
	mcpServer := newMCPServer(profile, "jackfield-workspace-gate")
	mcpServer.AddTool(
		mcp.NewTool("jackfield_gate_probe", mcp.WithDescription("Confirm that this MCP client belongs to the allowed workspace.")),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("Jackfield allowed this workspace."), nil
		},
	)
	return newHTTPHandler(mcpServer)
}

func NewProxyHandler(profile Profile, serverName string, upstream *Upstream, tools []mcp.Tool) http.Handler {
	mcpServer := newMCPServer(profile, serverName)
	addProbeTool(mcpServer)
	for _, tool := range tools {
		mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := upstream.CallTool(ctx, request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return result, nil
		})
	}
	return newHTTPHandler(mcpServer)
}

func addProbeTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool("jackfield_gate_probe", mcp.WithDescription("Confirm that this MCP client belongs to the allowed workspace. This tool does not contact Slack.")),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("Jackfield allowed this workspace."), nil
		},
	)
}

func newMCPServer(profile Profile, serverName string) *server.MCPServer {
	return server.NewMCPServer(
		serverName,
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithRoots(),
		server.WithToolHandlerMiddleware(authorizeTools(profile)),
	)
}

func newHTTPHandler(mcpServer *server.MCPServer) http.Handler {
	streamable := server.NewStreamableHTTPServer(mcpServer, server.WithEndpointPath("/mcp"))
	return localRequestsOnly(advertiseCodexSandboxCapability(streamable))
}

func localRequestsOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !localHostname(request.Host) {
			http.Error(response, "Jackfield accepts only local host names.", http.StatusForbidden)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !localHostname(parsed.Host) {
				http.Error(response, "Jackfield rejects non-local browser origins.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(response, request)
	})
}

func localHostname(hostPort string) bool {
	host := hostPort
	if parsedHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func authorizeTools(profile Profile) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			identity, err := discoverIdentity(ctx, request)
			if err != nil {
				return mcp.NewToolResultError("Jackfield denied this call. " + err.Error()), nil
			}
			if !profile.Allows(identity.Paths) {
				return mcp.NewToolResultErrorf(
					"Jackfield denied this call. The %s path is outside profile %q.",
					identity.Source,
					profile.Name,
				), nil
			}
			return next(ctx, request)
		}
	}
}

func advertiseCodexSandboxCapability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			next.ServeHTTP(response, request)
			return
		}

		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			http.Error(response, "The MCP request body is invalid.", http.StatusBadRequest)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		var envelope struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Method != "initialize" {
			next.ServeHTTP(response, request)
			return
		}

		captured := newCapturedResponse()
		next.ServeHTTP(captured, request)
		payload := addCodexCapability(captured.body.Bytes(), captured.Header().Get("Content-Type"))
		copyHeaders(response.Header(), captured.Header())
		response.Header().Del("Content-Length")
		response.WriteHeader(captured.statusCode)
		_, _ = response.Write(payload)
	})
}

func addCodexCapability(payload []byte, contentType string) []byte {
	if strings.Contains(contentType, "text/event-stream") {
		lines := strings.Split(string(payload), "\n")
		for index, line := range lines {
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				lines[index] = "data: " + string(addCodexCapabilityJSON([]byte(data)))
			}
		}
		return []byte(strings.Join(lines, "\n"))
	}
	return addCodexCapabilityJSON(payload)
}

func addCodexCapabilityJSON(payload []byte) []byte {
	var message map[string]any
	if json.Unmarshal(payload, &message) != nil {
		return payload
	}
	result, ok := message["result"].(map[string]any)
	if !ok {
		return payload
	}
	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		return payload
	}
	experimental, _ := capabilities["experimental"].(map[string]any)
	if experimental == nil {
		experimental = make(map[string]any)
	}
	experimental[codexSandboxMetaKey] = map[string]any{}
	capabilities["experimental"] = experimental
	rewritten, err := json.Marshal(message)
	if err != nil {
		return payload
	}
	return rewritten
}

type capturedResponse struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newCapturedResponse() *capturedResponse {
	return &capturedResponse{header: make(http.Header), statusCode: http.StatusOK}
}

func (response *capturedResponse) Header() http.Header { return response.header }

func (response *capturedResponse) WriteHeader(statusCode int) { response.statusCode = statusCode }

func (response *capturedResponse) Write(data []byte) (int, error) { return response.body.Write(data) }

func (response *capturedResponse) Flush() {}

func copyHeaders(destination http.Header, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}
