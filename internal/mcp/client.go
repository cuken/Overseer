package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cuken/overseer/pkg/types"
)

// Client manages connections to multiple MCP servers
type Client struct {
	servers    map[string]*ServerConnection
	mu         sync.RWMutex
}

// ServerConnection represents a connection to a single MCP server
type ServerConnection struct {
	name      string
	transport *Transport
	tools     []Tool
	connected bool
}

// Tool represents an MCP tool definition
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolsListResult represents the result of tools/list
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// ToolCallResult represents the result of tools/call
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents content in a tool result
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// NewClient creates a new MCP client
func NewClient() *Client {
	return &Client{
		servers: make(map[string]*ServerConnection),
	}
}

// Connect establishes connections to all configured MCP servers
func (c *Client) Connect(ctx context.Context, servers []types.MCPServer) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, cfg := range servers {
		if err := c.connectServer(ctx, cfg); err != nil {
			log.Printf("[MCP] Failed to connect to %s: %v", cfg.Name, err)
			continue
		}
		log.Printf("[MCP] Connected to %s", cfg.Name)
	}

	return nil
}

func (c *Client) connectServer(ctx context.Context, cfg types.MCPServer) error {
	transport, err := NewTransport(cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	conn := &ServerConnection{
		name:      cfg.Name,
		transport: transport,
	}

	// Initialize the connection
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "overseer",
			"version": "1.0.0",
		},
	}

	resp, err := transport.Send(initCtx, "initialize", initParams)
	if err != nil {
		transport.Close()
		return fmt.Errorf("initialize failed: %w", err)
	}

	if resp.Error != nil {
		transport.Close()
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Send initialized notification
	if err := transport.Notify("notifications/initialized", nil); err != nil {
		transport.Close()
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	// List available tools
	toolsResp, err := transport.Send(initCtx, "tools/list", nil)
	if err != nil {
		transport.Close()
		return fmt.Errorf("tools/list failed: %w", err)
	}

	if toolsResp.Error == nil {
		var result ToolsListResult
		if err := json.Unmarshal(toolsResp.Result, &result); err == nil {
			conn.tools = result.Tools
			log.Printf("[MCP] %s provides %d tools", cfg.Name, len(result.Tools))
		}
	}

	conn.connected = true
	c.servers[cfg.Name] = conn

	return nil
}

// CallTool executes a tool on the appropriate server
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*ToolCallResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Find server that provides this tool
	var targetConn *ServerConnection
	for _, conn := range c.servers {
		if !conn.connected {
			continue
		}
		for _, tool := range conn.tools {
			if tool.Name == name {
				targetConn = conn
				break
			}
		}
		if targetConn != nil {
			break
		}
	}

	if targetConn == nil {
		return nil, fmt.Errorf("no server provides tool: %s", name)
	}

	params := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}

	resp, err := targetConn.transport.Send(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	if resp.Error != nil {
		return &ToolCallResult{
			IsError: true,
			Content: []ContentBlock{{Type: "text", Text: resp.Error.Message}},
		}, nil
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	return &result, nil
}

// ListTools returns all available tools across all connected servers
func (c *Client) ListTools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var allTools []Tool
	for _, conn := range c.servers {
		if conn.connected {
			allTools = append(allTools, conn.tools...)
		}
	}
	return allTools
}

// IsConnected checks if any servers are connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, conn := range c.servers {
		if conn.connected {
			return true
		}
	}
	return false
}

// ServerStatus returns the status of all servers
func (c *Client) ServerStatus() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]bool)
	for name, conn := range c.servers {
		status[name] = conn.connected
	}
	return status
}

// Close disconnects all servers
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for _, conn := range c.servers {
		if conn.transport != nil {
			if err := conn.transport.Close(); err != nil {
				lastErr = err
			}
		}
	}
	c.servers = make(map[string]*ServerConnection)
	return lastErr
}
