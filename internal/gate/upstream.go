package gate

import (
	"context"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type Upstream struct {
	endpoint    string
	clientMu    sync.RWMutex
	client      *client.Client
	reconnectMu sync.Mutex
}

func ConnectUpstream(ctx context.Context, endpoint string) (*Upstream, []mcp.Tool, error) {
	mcpClient, tools, err := connectClient(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	return &Upstream{endpoint: endpoint, client: mcpClient}, tools, nil
}

func connectClient(ctx context.Context, endpoint string) (*client.Client, []mcp.Tool, error) {
	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("create upstream client: %w", err)
	}
	if err := mcpClient.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start upstream client: %w", err)
	}

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
	return mcpClient, result.Tools, nil
}

func (upstream *Upstream) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	failedClient := upstream.currentClient()
	result, err := callClientTool(ctx, failedClient, request)
	if err == nil {
		return result, nil
	}

	if reconnectErr := upstream.reconnect(ctx, failedClient); reconnectErr != nil {
		return nil, fmt.Errorf("the upstream MCP call failed and reconnection failed: %w", reconnectErr)
	}
	result, err = callClientTool(ctx, upstream.currentClient(), request)
	if err != nil {
		return nil, fmt.Errorf("the upstream MCP call failed after reconnection: %w", err)
	}
	return result, nil
}

func callClientTool(ctx context.Context, mcpClient *client.Client, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpClient.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      request.Params.Name,
		Arguments: request.Params.Arguments,
	}})
}

func (upstream *Upstream) currentClient() *client.Client {
	upstream.clientMu.RLock()
	defer upstream.clientMu.RUnlock()
	return upstream.client
}

func (upstream *Upstream) reconnect(ctx context.Context, failedClient *client.Client) error {
	upstream.reconnectMu.Lock()
	defer upstream.reconnectMu.Unlock()

	if upstream.currentClient() != failedClient {
		return nil
	}

	replacement, _, err := connectClient(ctx, upstream.endpoint)
	if err != nil {
		return err
	}

	upstream.clientMu.Lock()
	upstream.client = replacement
	upstream.clientMu.Unlock()
	_ = failedClient.Close()
	return nil
}

func (upstream *Upstream) Close() error {
	upstream.reconnectMu.Lock()
	defer upstream.reconnectMu.Unlock()
	return upstream.currentClient().Close()
}
