package llm

import "github.com/xiewanpeng/go-kimi/pkg/kimi/types"

// Message is one chat message in provider request history.
type Message struct {
	Role       string             `json:"role"`
	Content    types.ContentParts `json:"content,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

// ToolDefinition defines one callable tool for model-side tool use.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  types.JsonType `json:"parameters,omitempty"`
}

// ChatRequest is the normalized chat request contract for providers.
type ChatRequest struct {
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
}

// ChatResponse is the normalized non-stream chat response contract.
type ChatResponse struct {
	Content    types.ContentParts `json:"content,omitempty"`
	ToolCalls  []types.ToolCall   `json:"tool_calls,omitempty"`
	Usage      types.TokenUsage   `json:"usage,omitempty"`
	StopReason string             `json:"stop_reason,omitempty"`
}

// ChatEvent is one normalized stream event from provider responses.
type ChatEvent struct {
	Delta    types.ContentPart `json:"delta,omitempty"`
	ToolCall *types.ToolCall   `json:"tool_call,omitempty"`
	Usage    *types.TokenUsage `json:"usage,omitempty"`
	Done     bool              `json:"done,omitempty"`
	Err      error             `json:"-"`
}
