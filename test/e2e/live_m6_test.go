//go:build e2e_live

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	anthropicllm "github.com/xiewanpeng/go-kimi/pkg/kimi/llm/anthropic"
	geminillm "github.com/xiewanpeng/go-kimi/pkg/kimi/llm/gemini"
	openaillm "github.com/xiewanpeng/go-kimi/pkg/kimi/llm/openai"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveOpenAISingleTurn(t *testing.T) {
	provider, ok := m6OpenAIProviderFromEnv()
	if !ok {
		t.Skip("OPENAI_API_KEY is not set, skipping OpenAI live e2e")
	}

	m6RunLiveSingleTurn(t, provider, "LIVE_OPENAI_SINGLE_TURN_TOKEN_2026")
}

func TestLiveAnthropicSingleTurn(t *testing.T) {
	provider, ok := m6AnthropicProviderFromEnv()
	if !ok {
		t.Skip("ANTHROPIC_API_KEY is not set, skipping Anthropic live e2e")
	}

	m6RunLiveSingleTurn(t, provider, "LIVE_ANTHROPIC_SINGLE_TURN_TOKEN_2026")
}

func TestLiveGeminiSingleTurn(t *testing.T) {
	provider, ok := m6GeminiProviderFromEnv()
	if !ok {
		t.Skip("GEMINI_API_KEY is not set, skipping Gemini live e2e")
	}

	m6RunLiveSingleTurn(t, provider, "LIVE_GEMINI_SINGLE_TURN_TOKEN_2026")
}

func TestLiveProviderSwitch(t *testing.T) {
	providers := m6AvailableLiveProviders()
	if len(providers) < 2 {
		t.Skip("need at least two provider API keys among OPENAI_API_KEY/ANTHROPIC_API_KEY/GEMINI_API_KEY")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(providers[0].provider, ctxStore, nil, wire.NoopEmitter{}, "")

	firstToken := "LIVE_PROVIDER_SWITCH_FIRST_TOKEN_2026"
	firstResult, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Reply with this token only: " + firstToken},
	})
	if err != nil {
		t.Fatalf("first provider(%s) Run() error = %v", providers[0].name, err)
	}
	firstOutput := strings.TrimSpace(liveTextFromContentParts(firstResult.Content))
	if !containsCaseFold(firstOutput, firstToken) {
		t.Fatalf("first provider(%s) output = %q, want contains %q", providers[0].name, firstOutput, firstToken)
	}

	engine.SetProvider(providers[1].provider)

	secondToken := "LIVE_PROVIDER_SWITCH_SECOND_TOKEN_2026"
	secondResult, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Reply with this token only: " + secondToken},
	})
	if err != nil {
		t.Fatalf("second provider(%s) Run() error = %v", providers[1].name, err)
	}
	secondOutput := strings.TrimSpace(liveTextFromContentParts(secondResult.Content))
	if !containsCaseFold(secondOutput, secondToken) {
		t.Fatalf("second provider(%s) output = %q, want contains %q", providers[1].name, secondOutput, secondToken)
	}

	messages := ctxStore.Messages()
	if len(messages) < 4 {
		t.Fatalf("context message count after provider switch = %d, want >= 4", len(messages))
	}
	if messages[0].Role != soul.RoleUser {
		t.Fatalf("context first role = %q, want user", messages[0].Role)
	}
	if messages[len(messages)-1].Role != soul.RoleAssistant {
		t.Fatalf("context last role = %q, want assistant", messages[len(messages)-1].Role)
	}

	t.Logf("provider switch exercised: %s -> %s", providers[0].name, providers[1].name)
}

func m6RunLiveSingleTurn(t *testing.T, provider llm.ChatProvider, token string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")

	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Reply with this token only: " + token},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}

	messages := ctxStore.Messages()
	if len(messages) < 2 {
		t.Fatalf("context message count = %d, want >= 2", len(messages))
	}
	if messages[0].Role != soul.RoleUser {
		t.Fatalf("context first role = %q, want user", messages[0].Role)
	}
	if messages[len(messages)-1].Role != soul.RoleAssistant {
		t.Fatalf("context last role = %q, want assistant", messages[len(messages)-1].Role)
	}
}

type m6LiveProvider struct {
	name     string
	provider llm.ChatProvider
}

func m6AvailableLiveProviders() []m6LiveProvider {
	providers := make([]m6LiveProvider, 0, 3)
	if provider, ok := m6OpenAIProviderFromEnv(); ok {
		providers = append(providers, m6LiveProvider{name: "openai", provider: provider})
	}
	if provider, ok := m6AnthropicProviderFromEnv(); ok {
		providers = append(providers, m6LiveProvider{name: "anthropic", provider: provider})
	}
	if provider, ok := m6GeminiProviderFromEnv(); ok {
		providers = append(providers, m6LiveProvider{name: "gemini", provider: provider})
	}
	return providers
}

func m6OpenAIProviderFromEnv() (llm.ChatProvider, bool) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, false
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	provider := openaillm.NewOpenAIClient(apiKey, baseURL, model)
	if provider.ModelName() == "" {
		panic(fmt.Sprintf("openai provider model should not be empty: base_url=%q", baseURL))
	}
	return provider, true
}

func m6AnthropicProviderFromEnv() (llm.ChatProvider, bool) {
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return nil, false
	}
	baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
	provider := anthropicllm.NewAnthropicClient(apiKey, baseURL, model)
	if provider.ModelName() == "" {
		panic(fmt.Sprintf("anthropic provider model should not be empty: base_url=%q", baseURL))
	}
	return provider, true
}

func m6GeminiProviderFromEnv() (llm.ChatProvider, bool) {
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" {
		return nil, false
	}
	baseURL := strings.TrimSpace(os.Getenv("GEMINI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("GEMINI_MODEL"))
	provider := geminillm.NewGeminiClient(apiKey, baseURL, model)
	if provider.ModelName() == "" {
		panic(fmt.Sprintf("gemini provider model should not be empty: base_url=%q", baseURL))
	}
	return provider, true
}
