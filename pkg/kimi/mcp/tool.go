package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

var _ tools.Tool = (*MCPTool)(nil)

// MCPTool adapts one MCP tool definition into tools.Tool interface.
type MCPTool struct {
	client     *MCPClient
	definition MCPToolDefinition
	serverName string
}

// NewMCPTool creates one MCPTool adapter instance.
func NewMCPTool(client *MCPClient, definition MCPToolDefinition, serverName string) *MCPTool {
	return &MCPTool{
		client: client,
		definition: MCPToolDefinition{
			Name:        strings.TrimSpace(definition.Name),
			Description: strings.TrimSpace(definition.Description),
			InputSchema: cloneRawMessage(definition.InputSchema),
		},
		serverName: strings.TrimSpace(serverName),
	}
}

// Name returns model-facing tool name.
func (t *MCPTool) Name() string {
	if t == nil {
		return ""
	}
	toolName := strings.TrimSpace(t.definition.Name)
	if toolName == "" {
		return ""
	}
	if serverName := strings.TrimSpace(t.serverName); serverName != "" {
		return serverName + "__" + toolName
	}
	return toolName
}

// Description returns one short tool description.
func (t *MCPTool) Description() string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.definition.Description)
}

// ParameterSchema returns tool input schema.
func (t *MCPTool) ParameterSchema() json.RawMessage {
	if t == nil {
		return nil
	}
	return cloneRawMessage(t.definition.InputSchema)
}

// Execute forwards call to MCP tools/call and adapts result to types.ToolResult.
func (t *MCPTool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return types.ToolResult{}, errors.New("mcp tool: nil tool")
	}
	if t.client == nil {
		return types.ToolResult{}, fmt.Errorf("mcp tool %q: nil client", t.Name())
	}

	name := strings.TrimSpace(t.definition.Name)
	if name == "" {
		return types.ToolResult{}, fmt.Errorf("mcp tool %q: empty tool definition name", t.Name())
	}

	arguments, err := decodeToolArguments(params)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("mcp tool %q: %w", t.Name(), err)
	}

	callResult, err := t.client.CallTool(ctx, name, arguments)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("mcp tool %q: call tool %q: %w", t.Name(), name, err)
	}

	return types.ToolResult{
		Name: t.Name(),
		Value: types.ToolReturnValue{
			Value: mcpToolResultToValue(callResult),
		},
		IsError: callResult != nil && callResult.IsError,
	}, nil
}

func decodeToolArguments(raw json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, fmt.Errorf("decode params: %w", err)
	}
	if arguments == nil {
		return nil, nil
	}
	return arguments, nil
}

func mcpToolResultToValue(result *MCPToolResult) map[string]any {
	if result == nil {
		return map[string]any{
			"content": []map[string]any{},
			"isError": false,
		}
	}

	content := make([]map[string]any, 0, len(result.Content))
	for i := range result.Content {
		item := map[string]any{
			"type": strings.TrimSpace(result.Content[i].Type),
		}
		if text := strings.TrimSpace(result.Content[i].Text); text != "" {
			item["text"] = text
		}
		content = append(content, item)
	}

	return map[string]any{
		"content": content,
		"isError": result.IsError,
	}
}
