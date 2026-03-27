package llm

import "testing"

type fakeChatProvider struct{}

func (fakeChatProvider) ModelName() string {
	return "kimi-k2"
}

func (fakeChatProvider) WithThinking(_ string) ChatProvider {
	return fakeChatProvider{}
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
