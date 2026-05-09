package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	// MaxOutputChars limits tool text output size.
	MaxOutputChars = 50_000
	// MaxLineLengthChars limits one output line size.
	MaxLineLengthChars = 2_000
)

// Tool defines one model-callable tool.
type Tool interface {
	Name() string
	Description() string
	ParameterSchema() json.RawMessage
	Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error)
}

// ToolAdapter wraps Tool into soul.ToolExecutor.
type ToolAdapter struct {
	tool Tool
}

// NewToolAdapter creates one ToolAdapter.
func NewToolAdapter(tool Tool) soul.ToolExecutor {
	return ToolAdapter{tool: tool}
}

// Execute adapts soul tool-call arguments to raw JSON params.
func (a ToolAdapter) Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error) {
	if a.tool == nil {
		return types.ToolResult{}, errors.New("tools: nil tool")
	}

	params, err := toolCallArgumentsToRawMessage(call.Arguments)
	if err != nil {
		return types.ToolResult{}, err
	}

	return a.tool.Execute(ctx, params)
}

func toolCallArgumentsToRawMessage(arguments types.JsonType) (json.RawMessage, error) {
	switch typed := arguments.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		return normalizeRawJSON(typed)
	case []byte:
		return normalizeRawJSON(json.RawMessage(typed))
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return json.RawMessage(`{}`), nil
		}
		return normalizeRawJSON(json.RawMessage(text))
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("tools: marshal arguments: %w", err)
		}
		return normalizeRawJSON(encoded)
	}
}

func normalizeRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("tools: invalid json arguments: %q", trimmed)
	}
	out := make(json.RawMessage, len(trimmed))
	copy(out, trimmed)
	return out, nil
}
