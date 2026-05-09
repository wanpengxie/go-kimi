package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestNewProvider(t *testing.T) {
	t.Parallel()

	t.Run("registered constructor", func(t *testing.T) {
		t.Parallel()

		const customType ProviderType = "custom_test_provider"
		RegisterProviderConstructor(customType, func(cfg ProviderConfig) (ChatProvider, error) {
			return NewEchoChatProvider(cfg.Model), nil
		})

		provider, err := NewProvider(ProviderConfig{
			Type:  " CUSTOM_TEST_PROVIDER ",
			Model: "custom-echo-model",
		})
		if err != nil {
			t.Fatalf("NewProvider(custom) error = %v", err)
		}
		typed, ok := provider.(*EchoChatProvider)
		if !ok {
			t.Fatalf("provider type = %T, want *EchoChatProvider", provider)
		}
		if typed.ModelName() != "custom-echo-model" {
			t.Fatalf("ModelName() = %q, want %q", typed.ModelName(), "custom-echo-model")
		}
	})

	t.Run("moonshot aliases are recognized but not implemented", func(t *testing.T) {
		t.Parallel()

		tests := []string{"moonshot", "kimi"}
		for _, providerType := range tests {
			providerType := providerType
			t.Run(providerType, func(t *testing.T) {
				t.Parallel()

				_, err := NewProvider(ProviderConfig{
					Type:    providerType,
					APIKey:  "key",
					BaseURL: "https://example.com/v1",
					Model:   "kimi-k2",
				})
				if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
					t.Fatalf("NewProvider(%q) error = %v, want not implemented yet", providerType, err)
				}
			})
		}
	})

	t.Run("echo aliases", func(t *testing.T) {
		t.Parallel()

		tests := []string{"echo", "_echo"}
		for _, providerType := range tests {
			providerType := providerType
			t.Run(providerType, func(t *testing.T) {
				t.Parallel()

				provider, err := NewProvider(ProviderConfig{
					Type:  providerType,
					Model: "echo-model",
				})
				if err != nil {
					t.Fatalf("NewProvider(%q) error = %v", providerType, err)
				}
				typed, ok := provider.(*EchoChatProvider)
				if !ok {
					t.Fatalf("provider type = %T, want *EchoChatProvider", provider)
				}
				if typed.ModelName() != "echo-model" {
					t.Fatalf("ModelName() = %q, want %q", typed.ModelName(), "echo-model")
				}
			})
		}
	})

	t.Run("scripted echo aliases", func(t *testing.T) {
		t.Parallel()

		tests := []string{"scripted_echo", "_scripted_echo"}
		for _, providerType := range tests {
			providerType := providerType
			t.Run(providerType, func(t *testing.T) {
				t.Parallel()

				provider, err := NewProvider(ProviderConfig{
					Type: providerType,
					ScriptedResponses: []ChatResponse{
						{
							Content: types.ContentParts{
								types.TextPart{Text: "scripted"},
							},
							StopReason: "stop",
						},
					},
				})
				if err != nil {
					t.Fatalf("NewProvider(%q) error = %v", providerType, err)
				}
				typed, ok := provider.(*ScriptedEchoChatProvider)
				if !ok {
					t.Fatalf("provider type = %T, want *ScriptedEchoChatProvider", provider)
				}

				resp, err := typed.Chat(context.Background(), ChatRequest{})
				if err != nil {
					t.Fatalf("Chat() error = %v", err)
				}
				if len(resp.Content) != 1 {
					t.Fatalf("response content len = %d, want 1", len(resp.Content))
				}
				part, ok := resp.Content[0].(types.TextPart)
				if !ok || part.Text != "scripted" {
					t.Fatalf("response content[0] = %#v, want text scripted", resp.Content[0])
				}
			})
		}
	})

	t.Run("known but not implemented provider type", func(t *testing.T) {
		t.Parallel()

		_, err := NewProvider(ProviderConfig{Type: "openai"})
		if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
			t.Fatalf("NewProvider(openai) error = %v, want not implemented yet", err)
		}
	})

	t.Run("unsupported provider type", func(t *testing.T) {
		t.Parallel()

		_, err := NewProvider(ProviderConfig{Type: "unsupported_provider"})
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("NewProvider(unsupported_provider) error = %v, want not supported", err)
		}
	})
}
