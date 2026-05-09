// Example 01: Basic Turn — 最简 SDK 用法
//
// 演示：NewAgent → Run → 获取回复 → Close
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
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY is required")
		os.Exit(1)
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	if model == "" {
		model = "openai/gpt-4o-mini"
	}

	// 创建 provider
	provider, err := llm.NewProvider(llm.ProviderConfig{
		Type:    string(llm.ProviderTypeOpenAI),
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider error: %v\n", err)
		os.Exit(1)
	}

	// 创建 agent
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "你是一个友好的助手。用中文回答，简洁明了。",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	defer agent.Close()

	// 运行一个 turn
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "用一句话介绍 Go 语言的核心优势"); err != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		os.Exit(1)
	}

	// 获取结果
	result := agent.LastResult()
	fmt.Println(textFromParts(result.Content))
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
