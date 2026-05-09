package anthropic

import "github.com/wanpengxie/go-kimi/pkg/kimi/llm"

func init() {
	llm.RegisterProviderConstructor(llm.ProviderTypeAnthropic, newProviderFromFactoryConfig)
}

func newProviderFromFactoryConfig(cfg llm.ProviderConfig) (llm.ChatProvider, error) {
	return NewAnthropicClient(cfg.APIKey, cfg.BaseURL, cfg.Model), nil
}
