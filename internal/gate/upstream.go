package gate

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type Upstream struct {
	client *client.Client
}

func ConnectUpstream(ctx context.Context, endpoint string) (*Upstream, []mcp.Tool, error) {
	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("create upstream client: %w", err)
	}
	if err := mcpClient.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start upstream client: %w", err)
	}

	upstream := &Upstream{client: mcpClient}
	initialized := false
	defer func() {
		if !initialized {
			_ = mcpClient.Close()
		}
	}()

	_, err = mcpClient.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo: mcp.Implementation{
			Name:    "jackfield-workspace-gate",
			Version: "0.1.0",
		},
	}})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize upstream client: %w", err)
	}

	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("list upstream tools: %w", err)
	}
	initialized = true
	return upstream, result.Tools, nil
}

func (upstream *Upstream) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := upstream.client.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      request.Params.Name,
		Arguments: request.Params.Arguments,
	}})
	if err != nil {
		return nil, fmt.Errorf("the upstream MCP call failed: %w", err)
	}
	return result, nil
}

func (upstream *Upstream) Close() error {
	return upstream.client.Close()
}
