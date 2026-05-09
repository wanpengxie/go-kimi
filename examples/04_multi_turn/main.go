// Example 04: Multi-Turn — 多轮对话记忆
//
// 演示：多次 Run → 上下文自动累积 → LLM 记住之前的对话
package main

import (
	_ "github.com/wanpengxie/go-kimi/pkg/kimi/llm/openai"
	"context"
	"fmt"
	"os"
	"strings"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"time"

	kimi "github.com/wanpengxie/go-kimi/pkg/kimi"
	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
)

func main() {
	provider := mustProvider()

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "你是一个记忆力很好的助手。请简洁回答。",
		},
	})
	if err != nil {
		fatal("agent: %v", err)
	}
	defer agent.Close()

	turns := []string{
		"记住这个数字：42",
		"记住这个颜色：蓝色",
		"我刚才让你记住的数字和颜色分别是什么？",
	}

	for i, input := range turns {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		fmt.Printf("\n--- Turn %d ---\n", i+1)
		fmt.Printf("👤 %s\n", input)

		if err := agent.Run(ctx, input); err != nil {
			cancel()
			fatal("turn %d: %v", i+1, err)
		}
		cancel()

		result := agent.LastResult()
		fmt.Printf("🤖 %s\n", textFromParts(result.Content))
	}
}

func mustProvider() llm.ChatProvider {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		fatal("OPENAI_API_KEY required")
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	p, err := llm.NewProvider(llm.ProviderConfig{
		Type: string(llm.ProviderTypeOpenAI), BaseURL: base, APIKey: key, Model: model,
	})
	if err != nil {
		fatal("provider: %v", err)
	}
	return p
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func textFromParts(parts types.ContentParts) string {
	var b strings.Builder
	for _, p := range parts {
		if tp, ok := p.(types.TextPart); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}
