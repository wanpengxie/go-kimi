// Example 06: Error Handling — 错误类型系统
//
// 演示：errors.Is / errors.As 识别不同错误类型
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	kimi "github.com/wanpengxie/go-kimi/pkg/kimi"
	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
)

func main() {
	// --- Case 1: MaxStepsReached ---
	fmt.Println("=== Case 1: MaxStepsReached ===")
	runWithMaxSteps()

	// --- Case 2: RunCancelled ---
	fmt.Println("\n=== Case 2: RunCancelled ===")
	runWithTimeout()

	// --- Case 3: ToolNotFound ---
	fmt.Println("\n=== Case 3: ToolNotFound (via AllowedTools) ===")
	runWithMissingTool()
}

func runWithMaxSteps() {
	provider := mustEchoProvider()
	cfg := config.NewDefaultConfig()
	cfg.Loop.MaxTurns = 1 // 极低上限

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   cfg,
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "Always call the think tool.",
		},
	})
	if err != nil {
		fmt.Printf("  agent error: %v\n", err)
		return
	}
	defer agent.Close()

	ctx := context.Background()
	err = agent.Run(ctx, "hello")
	if err != nil {
		if errors.Is(err, kimierrors.ErrMaxStepsReached) {
			fmt.Println("  ✅ Caught ErrMaxStepsReached")
		} else {
			fmt.Printf("  Got: %v\n", err)
		}
	} else {
		fmt.Println("  (no error — echo provider didn't trigger tools)")
	}
}

func runWithTimeout() {
	provider := mustEchoProvider()

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: provider,
	})
	if err != nil {
		fmt.Printf("  agent error: %v\n", err)
		return
	}
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // 确保已超时

	err = agent.Run(ctx, "hello")
	if err != nil {
		if errors.Is(err, kimierrors.ErrRunCancelled) {
			fmt.Println("  ✅ Caught ErrRunCancelled")
		} else {
			fmt.Printf("  Got: %v\n", err)
		}
	}
}

func runWithMissingTool() {
	_, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: mustEchoProvider(),
		Overrides: kimi.AgentOverrides{
			AllowedTools: []string{"nonexistent_tool"},
		},
	})
	if err != nil {
		var toolErr *kimierrors.ToolError
		if errors.As(err, &toolErr) {
			fmt.Printf("  ✅ Caught ToolError: name=%q, cause=%v\n", toolErr.Name, toolErr.Cause)
		} else if errors.Is(err, kimierrors.ErrToolNotFound) {
			fmt.Println("  ✅ Caught ErrToolNotFound")
		} else {
			fmt.Printf("  Got: %v\n", err)
		}
	}
}

func mustEchoProvider() llm.ChatProvider {
	p, err := llm.NewProvider(llm.ProviderConfig{
		Type:  string(llm.ProviderTypeEcho),
		Model: "echo",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "echo provider: %v\n", err)
		os.Exit(1)
	}
	return p
}
