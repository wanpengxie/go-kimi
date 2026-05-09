package moonshot

import "github.com/wanpengxie/go-kimi/pkg/kimi/llm"

func init() {
	llm.RegisterProviderConstructor(llm.ProviderTypeMoonshot, newProviderFromFactoryConfig)
	llm.RegisterProviderConstructor(llm.ProviderTypeKimi, newProviderFromFactoryConfig)
}

func newProviderFromFactoryConfig(cfg llm.ProviderConfig) (llm.ChatProvider, error) {
	return NewMoonshotClient(cfg.APIKey, cfg.BaseURL, cfg.Model), nil
}
