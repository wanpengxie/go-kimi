package kimi

import (
	"context"
	stdErrors "errors"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/agentspec"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	kimierrors "github.com/xiewanpeng/go-kimi/pkg/kimi/errors"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestVersionIsSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestNewAgentRunCompactClose(t *testing.T) {
	t.Parallel()

	agent, err := NewAgent(AgentConfig{
		WorkDir: t.TempDir(),
		Config:  testRuntimeConfig(),
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	if err := agent.Run(context.Background(), "hello from test"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	result := agent.LastResult()
	if len(result.Content) == 0 {
		t.Fatalf("LastResult().Content is empty")
	}

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	if err := agent.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestNewAgentAllowedToolNotFound(t *testing.T) {
	t.Parallel()

	_, err := NewAgent(AgentConfig{
		WorkDir: t.TempDir(),
		Config:  testRuntimeConfig(),
		Spec: &agentspec.ResolvedSpec{
			Name:         "restricted",
			AllowedTools: []string{"does_not_exist"},
		},
	})
	if err == nil {
		t.Fatal("NewAgent() error = nil, want tool-not-found")
	}

	var toolErr *kimierrors.ToolError
	if !stdErrors.As(err, &toolErr) {
		t.Fatalf("NewAgent() error = %v, want *ToolError", err)
	}
	if !stdErrors.Is(err, kimierrors.ErrToolNotFound) {
		t.Fatalf("NewAgent() error = %v, want ErrToolNotFound", err)
	}
}

func TestAgentRunReturnsMaxStepsReached(t *testing.T) {
	t.Parallel()

	runtimeCfg := testRuntimeConfig()
	runtimeCfg.Loop.MaxTurns = 1

	agent, err := NewAgent(AgentConfig{
		WorkDir:  t.TempDir(),
		Config:   runtimeCfg,
		Provider: &loopingProvider{model: "loop-model"},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	err = agent.Run(context.Background(), "trigger loop")
	if !stdErrors.Is(err, kimierrors.ErrMaxStepsReached) {
		t.Fatalf("Run() error = %v, want ErrMaxStepsReached", err)
	}
}

func TestCompositeEmitter_PartialFailure(t *testing.T) {
	t.Parallel()

	fallbackErr := stdErrors.New("fallback failed")
	fallback := &testWireEmitter{err: fallbackErr}
	primary := &testWireEmitter{}

	emitter := compositeEmitter{emitters: []wire.Emitter{fallback, primary}}
	err := emitter.Emit(wire.TurnBegin{TurnID: "turn-1"})
	if err == nil {
		t.Fatal("Emit() error = nil, want joined fallback error")
	}
	if !stdErrors.Is(err, fallbackErr) {
		t.Fatalf("Emit() error = %v, want contains fallback error", err)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1 (must still run after fallback error)", primary.calls)
	}
}

func testRuntimeConfig() config.Config {
	cfg := config.NewDefaultConfig()
	cfg.Providers = []config.LLMProvider{
		{Name: "echo", Type: "echo"},
	}
	cfg.Models = []config.LLMModel{
		{Name: "echo-model", Provider: "echo"},
	}
	cfg.DefaultProvider = "echo"
	cfg.DefaultModel = "echo-model"
	cfg.Services.MoonshotFetch.Enabled = false
	cfg.Services.MoonshotSearch.Enabled = false
	cfg.MCP.Clients = nil
	return cfg
}

type loopingProvider struct {
	model string
}

func (p *loopingProvider) ModelName() string {
	if p == nil {
		return ""
	}
	return p.model
}

func (p *loopingProvider) WithModel(model string) llm.ChatProvider {
	if p == nil {
		return p
	}
	clone := *p
	clone.model = model
	return &clone
}

func (p *loopingProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: types.ContentParts{types.TextPart{Text: "loop"}},
	}, nil
}

func (p *loopingProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	ch := make(chan llm.ChatEvent, 2)
	ch <- llm.ChatEvent{ToolCall: &types.ToolCall{ID: "call-loop", Name: "loop_tool"}}
	ch <- llm.ChatEvent{Done: true}
	close(ch)
	return ch, nil
}

type testWireEmitter struct {
	calls int
	err   error
}

func (e *testWireEmitter) Emit(_ wire.WireMessage) error {
	if e == nil {
		return nil
	}
	e.calls++
	return e.err
}
