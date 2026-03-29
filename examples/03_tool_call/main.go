// Example 03: Tool Call — 让 LLM 调用工具
//
// 演示：Agent + Shell 工具 → LLM 自主决定调用 → 返回结果
package main

import (
	_ "github.com/xiewanpeng/go-kimi/pkg/kimi/llm/openai"
	"context"
	"fmt"
	"os"
	"strings"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"time"

	kimi "github.com/xiewanpeng/go-kimi/pkg/kimi"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
)

func main() {
	provider := mustProvider()

	yolo := true
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "你是一个 Linux 系统管理员。你可以使用 shell 工具执行命令。",
			DefaultYolo:  &yolo, // 自动审批所有工具调用
		},
	})
	if err != nil {
		fatal("agent: %v", err)
	}
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "查看当前系统的 Go 版本和内核版本，用 shell 工具执行 'go version' 和 'uname -r'"); err != nil {
		fatal("run: %v", err)
	}

	result := agent.LastResult()
	fmt.Println("=== Agent 回复 ===")
	fmt.Println(textFromParts(result.Content))

	if len(result.ToolCalls) > 0 {
		fmt.Printf("\n=== 工具调用 (%d 次) ===\n", len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			fmt.Printf("  → %s\n", tc.Name)
		}
	}
	if len(result.ToolResults) > 0 {
		fmt.Printf("\n=== 工具结果 (%d 个) ===\n", len(result.ToolResults))
		for _, tr := range result.ToolResults {
			val := fmt.Sprintf("%v", tr.Value.Value)
			if len(val) > 200 {
				val = val[:200] + "..."
			}
			fmt.Printf("  ← %s: %s\n", tr.Name, strings.TrimSpace(val))
		}
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
