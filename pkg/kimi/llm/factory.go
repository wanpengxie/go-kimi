package llm

import (
	"fmt"
	"strings"
	"sync"
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

// ProviderConstructor builds one provider from config.
type ProviderConstructor func(cfg ProviderConfig) (ChatProvider, error)

var (
	providerConstructorsMu sync.RWMutex
	providerConstructors   = map[ProviderType]ProviderConstructor{}
)

// RegisterProviderConstructor registers constructor for one provider type.
func RegisterProviderConstructor(providerType ProviderType, constructor ProviderConstructor) {
	if constructor == nil {
		panic("llm: provider constructor is nil")
	}

	normalized := normalizeProviderType(string(providerType))
	providerConstructorsMu.Lock()
	providerConstructors[normalized] = constructor
	providerConstructorsMu.Unlock()
}

// NewProvider creates one ChatProvider by provider type.
func NewProvider(cfg ProviderConfig) (ChatProvider, error) {
	providerType := normalizeProviderType(cfg.Type)
	switch providerType {
	case ProviderTypeEcho:
		return NewEchoChatProvider(cfg.Model), nil
	case ProviderTypeScriptedEcho:
		return NewScriptedEchoChatProvider(cfg.Model, cfg.ScriptedResponses), nil
	}

	if constructor := providerConstructor(providerType); constructor != nil {
		provider, err := constructor(cfg)
		if err != nil {
			return nil, err
		}
		if provider == nil {
			return nil, fmt.Errorf("llm provider %q constructor returned nil", providerType)
		}
		return provider, nil
	}

	switch providerType {
	case ProviderTypeMoonshot, ProviderTypeOpenAI, ProviderTypeAnthropic, ProviderTypeGemini, ProviderTypeAzureOpenAI, ProviderTypeDeepSeek:
		return nil, fmt.Errorf("llm provider %q is not implemented yet", providerType)
	default:
		return nil, fmt.Errorf("llm provider %q is not supported", strings.TrimSpace(cfg.Type))
	}
}

func providerConstructor(providerType ProviderType) ProviderConstructor {
	providerConstructorsMu.RLock()
	constructor := providerConstructors[providerType]
	providerConstructorsMu.RUnlock()
	return constructor
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
