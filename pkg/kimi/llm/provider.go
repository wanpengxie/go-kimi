package llm

import "context"

// ChatProvider defines the provider-facing chat abstraction.
type ChatProvider interface {
	ModelName() string
	WithThinking(effort string) ChatProvider
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}
