package llm

import (
	"context"
	"testing"
)

type fakeChatProvider struct{}

func (fakeChatProvider) ModelName() string {
	return "kimi-k2"
}

func (fakeChatProvider) WithThinking(_ string) ChatProvider {
	return fakeChatProvider{}
}

func (fakeChatProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}

func (fakeChatProvider) ChatStream(_ context.Context, _ ChatRequest) (<-chan ChatEvent, error) {
	ch := make(chan ChatEvent)
	close(ch)
	return ch, nil
}

var _ ChatProvider = fakeChatProvider{}

func TestChatProviderContract(t *testing.T) {
	t.Parallel()

	var provider ChatProvider = fakeChatProvider{}
	if provider.ModelName() != "kimi-k2" {
		t.Fatalf("ModelName() = %q, want %q", provider.ModelName(), "kimi-k2")
	}
	if provider.WithThinking("high") == nil {
		t.Fatal("WithThinking() returned nil provider")
	}
}
