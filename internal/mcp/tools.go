package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/cuken/overseer/internal/agent"
	"github.com/cuken/overseer/pkg/types"
)

// ToolExecutor implements agent.ToolExecutor using MCP
type ToolExecutor struct {
	client *Client
}

// NewToolExecutor creates a new tool executor backed by MCP
func NewToolExecutor(client *Client) *ToolExecutor {
	return &ToolExecutor{client: client}
}

// Execute implements agent.ToolExecutor
func (e *ToolExecutor) Execute(ctx context.Context, call types.ToolCall) types.ToolResult {
	result := types.ToolResult{
		CallID: call.ID,
	}

	if e.client == nil || !e.client.IsConnected() {
		result.Success = false
		result.Error = "MCP client not connected"
		return result
	}

	mcpResult, err := e.client.CallTool(ctx, call.Name, call.Arguments)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	if mcpResult.IsError {
		result.Success = false
		if len(mcpResult.Content) > 0 {
			result.Error = mcpResult.Content[0].Text
		} else {
			result.Error = "unknown error"
		}
		return result
	}

	// Concatenate all text content
	var output strings.Builder
	for _, content := range mcpResult.Content {
		if content.Type == "text" {
			output.WriteString(content.Text)
		}
	}

	result.Success = true
	result.Output = output.String()
	return result
}

// AvailableTools implements agent.ToolExecutor
func (e *ToolExecutor) AvailableTools() []agent.ToolInfo {
	if e.client == nil {
		return nil
	}

	mcpTools := e.client.ListTools()
	tools := make([]agent.ToolInfo, 0, len(mcpTools))

	for _, t := range mcpTools {
		info := agent.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
		}

		// Format input schema as string
		if t.InputSchema != nil {
			info.Parameters = formatSchema(t.InputSchema)
		}

		tools = append(tools, info)
	}

	return tools
}

// formatSchema converts a JSON schema to a readable string
func formatSchema(schema map[string]interface{}) string {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return "{}"
	}

	required := make(map[string]bool)
	if reqList, ok := schema["required"].([]interface{}); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	var sb strings.Builder
	for name, prop := range props {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}

		propType := "any"
		if t, ok := propMap["type"].(string); ok {
			propType = t
		}

		desc := ""
		if d, ok := propMap["description"].(string); ok {
			desc = d
		}

		reqMarker := ""
		if required[name] {
			reqMarker = " (required)"
		}

		sb.WriteString(fmt.Sprintf("  %s: %s%s", name, propType, reqMarker))
		if desc != "" {
			sb.WriteString(fmt.Sprintf(" - %s", desc))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// Verify ToolExecutor implements the interface
var _ agent.ToolExecutor = (*ToolExecutor)(nil)
