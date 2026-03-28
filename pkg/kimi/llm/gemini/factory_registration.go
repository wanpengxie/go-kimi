package gemini

import "github.com/xiewanpeng/go-kimi/pkg/kimi/llm"

func init() {
	llm.RegisterProviderConstructor(llm.ProviderTypeGemini, newProviderFromFactoryConfig)
	llm.RegisterProviderConstructor(llm.ProviderTypeGoogle, newProviderFromFactoryConfig)
}

func newProviderFromFactoryConfig(cfg llm.ProviderConfig) (llm.ChatProvider, error) {
	return NewGeminiClient(cfg.APIKey, cfg.BaseURL, cfg.Model), nil
}
