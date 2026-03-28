package llm

import (
	"context"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const defaultEchoModel = "echo"

// EchoChatProvider mirrors the latest input text back to callers.
type EchoChatProvider struct {
	model          string
	thinkingEffort ThinkingEffort
}

var _ ChatProvider = (*EchoChatProvider)(nil)
var _ ThinkingProvider = (*EchoChatProvider)(nil)

// NewEchoChatProvider creates one deterministic echo provider.
func NewEchoChatProvider(model string) *EchoChatProvider {
	return &EchoChatProvider{
		model:          normalizeEchoModel(model, defaultEchoModel),
		thinkingEffort: ThinkingOff,
	}
}

// ModelName returns the configured model identifier.
func (p *EchoChatProvider) ModelName() string {
	if p == nil {
		return ""
	}
	return p.model
}

// WithModel clones the provider with a different model identifier.
func (p *EchoChatProvider) WithModel(model string) ChatProvider {
	if p == nil {
		return p
	}
	cloned := *p
	cloned.model = normalizeEchoModel(model, p.model)
	return &cloned
}

// WithThinking clones the provider with a normalized thinking effort value.
func (p *EchoChatProvider) WithThinking(effort ThinkingEffort) ChatProvider {
	if p == nil {
		return p
	}
	cloned := *p
	cloned.thinkingEffort = NormalizeThinkingEffort(effort)
	return &cloned
}

// Chat returns one single text message that echoes input text.
func (p *EchoChatProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content: types.ContentParts{
			types.TextPart{Text: extractEchoText(req)},
		},
		StopReason: "stop",
	}, nil
}

// ChatStream emits one text delta per rune, then finishes.
func (p *EchoChatProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	text := extractEchoText(req)
	ch := make(chan ChatEvent)
	go func() {
		defer close(ch)
		for _, r := range text {
			if !emitChatEvent(ctx, ch, ChatEvent{Delta: types.TextPart{Text: string(r)}}) {
				return
			}
		}
		_ = emitChatEvent(ctx, ch, ChatEvent{Done: true})
	}()

	return ch, nil
}

func extractEchoText(req ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			text := messageText(msg)
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
	}

	for i := len(req.Messages) - 1; i >= 0; i-- {
		text := messageText(req.Messages[i])
		if strings.TrimSpace(text) != "" {
			return text
		}
	}

	return ""
}

func messageText(msg Message) string {
	if len(msg.Content) == 0 {
		return ""
	}

	var builder strings.Builder
	for i := range msg.Content {
		switch part := msg.Content[i].(type) {
		case types.TextPart:
			builder.WriteString(part.Text)
		case *types.TextPart:
			if part != nil {
				builder.WriteString(part.Text)
			}
		}
	}
	return builder.String()
}

func emitChatEvent(ctx context.Context, out chan<- ChatEvent, event ChatEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func normalizeEchoModel(model string, fallback string) string {
	normalized := strings.TrimSpace(model)
	if normalized != "" {
		return normalized
	}
	normalizedFallback := strings.TrimSpace(fallback)
	if normalizedFallback != "" {
		return normalizedFallback
	}
	return defaultEchoModel
}
