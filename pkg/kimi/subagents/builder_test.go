package subagents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func TestBuildInheritPolicyCreatesIsolatedSoulRuntime(t *testing.T) {
	t.Parallel()

	contextDir := filepath.Join(t.TempDir(), "agent-1")
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "hello"}},
				{Done: true},
			},
		},
	}
	parent := mockToolRegistry{
		definitions: []llm.ToolDefinition{
			{Name: "think"},
			{Name: "shell"},
		},
		executors: map[string]toolExecutorFunc{
			"think": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{}, nil
			},
			"shell": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{}, nil
			},
		},
	}

	engine, err := Build(&AgentTypeDefinition{
		Name: "planner",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyInherit,
		},
	}, BuildConfig{
		Provider:       provider,
		SystemPrompt:   "system",
		ParentRegistry: parent,
	}, contextDir)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if _, err := engine.Run(context.Background(), types.ContentParts{types.TextPart{Text: "run"}}); err != nil {
		t.Fatalf("engine.Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(requests[0].Tools))
	}
	if requests[0].Tools[0].Name != "shell" || requests[0].Tools[1].Name != "think" {
		t.Fatalf("tool names = [%q %q], want [shell think]", requests[0].Tools[0].Name, requests[0].Tools[1].Name)
	}

	contextPath := filepath.Join(contextDir, contextFileName)
	contextPayload, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("os.ReadFile(context) error = %v", err)
	}
	if len(contextPayload) == 0 {
		t.Fatal("context.jsonl is empty, want persisted run history")
	}

	wirePath := filepath.Join(contextDir, wireFileName)
	if count := countWireRecords(t, wirePath); count == 0 {
		t.Fatal("wire.jsonl record count = 0, want > 0")
	}
}

func TestBuildAllowlistPolicyFiltersParentRegistry(t *testing.T) {
	t.Parallel()

	provider := &scriptedChatProvider{}
	parent := mockToolRegistry{
		definitions: []llm.ToolDefinition{{Name: "shell"}, {Name: "think"}},
		executors: map[string]toolExecutorFunc{
			"shell": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{}, nil
			},
			"think": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{}, nil
			},
		},
	}

	engine, err := Build(&AgentTypeDefinition{
		Name: "planner",
		ToolPolicy: ToolPolicy{
			Mode:      ToolPolicyAllowlist,
			Allowlist: []string{"shell", " shell ", ""},
		},
	}, BuildConfig{
		Provider:       provider,
		ParentRegistry: parent,
	}, filepath.Join(t.TempDir(), "agent-allowlist"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if _, err := engine.Run(context.Background(), types.ContentParts{types.TextPart{Text: "run"}}); err != nil {
		t.Fatalf("engine.Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "shell" {
		t.Fatalf("allowlist tools = %#v, want one shell tool", requests[0].Tools)
	}

	registry := engine.ToolRegistry()
	if _, ok := registry.Executor("shell"); !ok {
		t.Fatal("registry.Executor(shell) ok = false, want true")
	}
	if _, ok := registry.Executor("think"); ok {
		t.Fatal("registry.Executor(think) ok = true, want false")
	}
}

func TestBuildValidationErrors(t *testing.T) {
	t.Parallel()

	provider := &scriptedChatProvider{}

	if _, err := Build(nil, BuildConfig{Provider: provider}, t.TempDir()); err == nil {
		t.Fatal("Build(nil definition) error = nil, want error")
	}

	if _, err := Build(&AgentTypeDefinition{Name: "planner"}, BuildConfig{}, t.TempDir()); err == nil {
		t.Fatal("Build(nil provider) error = nil, want error")
	}

	if _, err := Build(&AgentTypeDefinition{Name: "planner"}, BuildConfig{Provider: provider}, "  "); err == nil {
		t.Fatal("Build(empty context dir) error = nil, want error")
	}

	_, err := Build(&AgentTypeDefinition{
		Name: "planner",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyMode("invalid"),
		},
	}, BuildConfig{Provider: provider, ParentRegistry: mockToolRegistry{}}, t.TempDir())
	if err == nil {
		t.Fatal("Build(invalid tool policy mode) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported tool policy") {
		t.Fatalf("Build(invalid policy) error = %v, want unsupported policy", err)
	}
}

func countWireRecords(t *testing.T, wirePath string) int {
	t.Helper()

	iter, err := wire.NewWireFile(wirePath).IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() error = %v", err)
	}
	defer func() {
		_ = iter.Close()
	}()

	count := 0
	for iter.Next() {
		count++
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("wire iterator error = %v", err)
	}
	return count
}
