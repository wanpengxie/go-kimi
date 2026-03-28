package llm

import (
	"fmt"
	"strings"
)

// ProviderConfig defines one provider construction request.
type ProviderConfig struct {
	Type              string            `json:"type"`
	BaseURL           string            `json:"base_url,omitempty"`
	APIKey            string            `json:"api_key,omitempty"`
	Model             string            `json:"model,omitempty"`
	MaxContext        int               `json:"max_context,omitempty"`
	ExtraHeaders      map[string]string `json:"extra_headers,omitempty"`
	ScriptedResponses []ChatResponse    `json:"scripted_responses,omitempty"`
}

// NewProvider creates one ChatProvider by provider type.
func NewProvider(cfg ProviderConfig) (ChatProvider, error) {
	providerType := normalizeProviderType(cfg.Type)
	switch providerType {
	case ProviderTypeEcho:
		return NewEchoChatProvider(cfg.Model), nil
	case ProviderTypeScriptedEcho:
		return NewScriptedEchoChatProvider(cfg.Model, cfg.ScriptedResponses), nil
	case ProviderTypeMoonshot, ProviderTypeOpenAI, ProviderTypeAnthropic, ProviderTypeGemini, ProviderTypeAzureOpenAI, ProviderTypeDeepSeek:
		return nil, fmt.Errorf("llm provider %q is not implemented yet", providerType)
	default:
		return nil, fmt.Errorf("llm provider %q is not supported", strings.TrimSpace(cfg.Type))
	}
}

func normalizeProviderType(raw string) ProviderType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ProviderTypeKimi), string(ProviderTypeMoonshot):
		return ProviderTypeMoonshot
	case string(ProviderTypeOpenAI):
		return ProviderTypeOpenAI
	case string(ProviderTypeAnthropic):
		return ProviderTypeAnthropic
	case string(ProviderTypeGemini), string(ProviderTypeGoogle):
		return ProviderTypeGemini
	case string(ProviderTypeAzureOpenAI):
		return ProviderTypeAzureOpenAI
	case string(ProviderTypeDeepSeek):
		return ProviderTypeDeepSeek
	case string(ProviderTypeEcho), "_echo":
		return ProviderTypeEcho
	case string(ProviderTypeScriptedEcho), "_scripted_echo":
		return ProviderTypeScriptedEcho
	default:
		return ProviderType(strings.ToLower(strings.TrimSpace(raw)))
	}
}
