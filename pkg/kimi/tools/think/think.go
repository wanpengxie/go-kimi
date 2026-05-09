package think

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	toolName        = "think"
	toolDescription = "Record internal reasoning without emitting output."
)

var parameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "thought": {
      "type": "string",
      "description": "Internal reasoning content"
    }
  },
  "required": ["thought"],
  "additionalProperties": false
}`)

// Tool implements the think core tool.
type Tool struct{}

// New creates one think tool.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool name.
func (*Tool) Name() string {
	return toolName
}

// Description returns the tool description.
func (*Tool) Description() string {
	return toolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*Tool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(parameterSchema)
}

// Execute validates params and returns an empty output payload.
func (*Tool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	var input struct {
		Thought string `json:"thought"`
	}
	if text := strings.TrimSpace(string(params)); text != "" && text != "null" {
		if err := json.Unmarshal(params, &input); err != nil {
			return types.ToolResult{}, fmt.Errorf("think tool: decode params: %w", err)
		}
	}

	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: "",
		},
	}, nil
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
