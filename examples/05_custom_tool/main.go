// Example 05: Custom Tool — 自定义工具
//
// 演示：实现 tools.Tool 接口 → 注册到 Agent → LLM 调用
package main

import (
	_ "github.com/xiewanpeng/go-kimi/pkg/kimi/llm/openai"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	kimi "github.com/xiewanpeng/go-kimi/pkg/kimi"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

// CalculatorTool 是一个自定义计算器工具
type CalculatorTool struct{}

func (t *CalculatorTool) Name() string        { return "calculator" }
func (t *CalculatorTool) Description() string  { return "Perform a mathematical calculation. Supports add, subtract, multiply, divide, sqrt, power." }
func (t *CalculatorTool) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"operation": {"type": "string", "enum": ["add","subtract","multiply","divide","sqrt","power"]},
			"a": {"type": "number"},
			"b": {"type": "number"}
		},
		"required": ["operation", "a"]
	}`)
}

func (t *CalculatorTool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	var p struct {
		Op string  `json:"operation"`
		A  float64 `json:"a"`
		B  float64 `json:"b"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return types.ToolResult{IsError: true, Value: types.ToolReturnValue{Value: "invalid params"}}, nil
	}

	var result float64
	switch p.Op {
	case "add":
		result = p.A + p.B
	case "subtract":
		result = p.A - p.B
	case "multiply":
		result = p.A * p.B
	case "divide":
		if p.B == 0 {
			return types.ToolResult{IsError: true, Value: types.ToolReturnValue{Value: "division by zero"}}, nil
		}
		result = p.A / p.B
	case "sqrt":
		result = math.Sqrt(p.A)
	case "power":
		result = math.Pow(p.A, p.B)
	default:
		return types.ToolResult{IsError: true, Value: types.ToolReturnValue{Value: "unknown operation"}}, nil
	}

	return types.ToolResult{
		Value: types.ToolReturnValue{Value: fmt.Sprintf("%.6g", result)},
	}, nil
}

func main() {
	provider := mustProvider()

	yolo := true
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  os.TempDir(),
		Config:   config.NewDefaultConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			SystemPrompt: "你是一个数学助手。使用 calculator 工具来计算。",
			DefaultYolo:  &yolo,
		},
		AdditionalTools: []tools.Tool{&CalculatorTool{}},
	})
	if err != nil {
		fatal("agent: %v", err)
	}
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "计算 (123 * 456) + sqrt(65536)，分步用 calculator 工具"); err != nil {
		fatal("run: %v", err)
	}

	result := agent.LastResult()
	fmt.Println(textFromParts(result.Content))
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
