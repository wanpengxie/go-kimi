// Example 02: Streaming — 实时流式输出
//
// 演示：Wire Hub 订阅 → 逐 chunk 打印 TextDelta
package main

import (
	_ "github.com/wanpengxie/go-kimi/pkg/kimi/llm/openai"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	kimi "github.com/wanpengxie/go-kimi/pkg/kimi"
	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func main() {
	provider := mustProvider()

	// 用 ChannelEmitter 接收 wire 事件
	ch := make(chan wire.WireMessage, 256)
	emitter := &wire.ChannelEmitter{Ch: ch}

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:     os.TempDir(),
		Config:      config.NewDefaultConfig(),
		Provider:    provider,
		WireEmitter: emitter,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "你是一个诗人。",
		},
	})
	if err != nil {
		fatal("agent: %v", err)
	}
	defer agent.Close()

	// 后台消费 wire 事件
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			switch m := msg.(type) {
			case wire.TurnBegin:
				fmt.Print("\n🤖 ")
			case wire.TextDelta:
				fmt.Print(m.Delta)
			case wire.TurnEnd:
				fmt.Printf("\n\n[stop=%s, tokens=%d]\n", m.StopReason, m.Usage.TotalTokens)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "写一首关于并发的五言绝句"); err != nil {
		fatal("run: %v", err)
	}

	wg.Wait()
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
