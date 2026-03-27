package subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestForegroundSubagentRunnerRunCreatesNewInstance(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name:         "planner",
		DefaultModel: "kimi-k2",
		ToolPolicy: ToolPolicy{
			Mode:      ToolPolicyAllowlist,
			Allowlist: []string{"shell"},
		},
	})

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "subagent output"}},
				{Done: true},
			},
		},
	}
	runner := NewForegroundSubagentRunner(RunnerDeps{
		Market:   market,
		Store:    store,
		Provider: provider,
		ParentRegistry: mockToolRegistry{
			definitions: []llm.ToolDefinition{{Name: "shell"}, {Name: "think"}},
			executors: map[string]toolExecutorFunc{
				"shell": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
					return types.ToolResult{}, nil
				},
				"think": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
					return types.ToolResult{}, nil
				},
			},
		},
		SystemPrompt: "root system",
	})

	ret, err := runner.Run(context.Background(), ForegroundRunRequest{
		SubagentType:  "planner",
		Prompt:        "Plan the rollout",
		ModelOverride: "kimi-k2.5",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run() return type = %T, want map[string]any", ret.Value)
	}
	agentID, _ := payload["agent_id"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("Run() agent_id = %q, want non-empty", agentID)
	}
	if got, _ := payload["subagent_type"].(string); got != "planner" {
		t.Fatalf("Run() subagent_type = %q, want planner", got)
	}
	if got, _ := payload["status"].(string); got != string(StatusIdle) {
		t.Fatalf("Run() status = %q, want %q", got, StatusIdle)
	}
	if got, _ := payload["output_text"].(string); got != "subagent output" {
		t.Fatalf("Run() output_text = %q, want %q", got, "subagent output")
	}

	record, err := store.Get(agentID)
	if err != nil {
		t.Fatalf("store.Get(%q) error = %v", agentID, err)
	}
	if record.Status != StatusIdle {
		t.Fatalf("record.Status = %q, want %q", record.Status, StatusIdle)
	}
	if record.Description != "Plan the rollout" {
		t.Fatalf("record.Description = %q, want prompt", record.Description)
	}
	if record.LaunchSpec.ModelOverride != "kimi-k2.5" {
		t.Fatalf("record.LaunchSpec.ModelOverride = %q, want kimi-k2.5", record.LaunchSpec.ModelOverride)
	}
	if record.LaunchSpec.EffectiveModel != "kimi-k2.5" {
		t.Fatalf("record.LaunchSpec.EffectiveModel = %q, want kimi-k2.5", record.LaunchSpec.EffectiveModel)
	}

	promptPath := filepath.Join(filepath.Dir(filepath.Dir(recordPathFor(store, record.AgentID))), record.AgentID, promptFileName)
	promptPayload, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("os.ReadFile(prompt.txt) error = %v", err)
	}
	if string(promptPayload) != "Plan the rollout" {
		t.Fatalf("prompt.txt = %q, want %q", string(promptPayload), "Plan the rollout")
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "shell" {
		t.Fatalf("request tools = %#v, want one shell tool", requests[0].Tools)
	}

	wirePath := filepath.Join(filepath.Dir(filepath.Dir(recordPathFor(store, record.AgentID))), record.AgentID, wireFileName)
	if count := countWireRecords(t, wirePath); count == 0 {
		t.Fatal("wire records = 0, want > 0")
	}
}

func TestForegroundSubagentRunnerRunResumesExistingInstance(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	base := &AgentInstanceRecord{
		AgentID:      "agent-resume",
		SubagentType: "planner",
		Status:       StatusIdle,
		Description:  "old",
		CreatedAt:    10,
		UpdatedAt:    10,
		LaunchSpec: AgentLaunchSpec{
			CreatedAt: 10,
		},
	}
	if err := store.Create(base); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name:         "planner",
		DefaultModel: "kimi-k2",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyInherit,
		},
	})

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: "resumed output"}},
			{Done: true},
		}},
	}
	runner := NewForegroundSubagentRunner(RunnerDeps{
		Market:         market,
		Store:          store,
		Provider:       provider,
		ParentRegistry: mockToolRegistry{},
	})

	ret, err := runner.Run(context.Background(), ForegroundRunRequest{
		AgentID: " agent-resume ",
		Prompt:  "continue work",
	})
	if err != nil {
		t.Fatalf("Run(resume) error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run(resume) return type = %T, want map[string]any", ret.Value)
	}
	if got, _ := payload["agent_id"].(string); got != "agent-resume" {
		t.Fatalf("Run(resume) agent_id = %q, want agent-resume", got)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(store.List()) = %d, want 1", len(list))
	}
	updated := list[0]
	if updated.Status != StatusIdle {
		t.Fatalf("updated.Status = %q, want %q", updated.Status, StatusIdle)
	}
	if updated.Description != "continue work" {
		t.Fatalf("updated.Description = %q, want continue work", updated.Description)
	}
	if updated.UpdatedAt <= 10 {
		t.Fatalf("updated.UpdatedAt = %f, want > 10", updated.UpdatedAt)
	}
}

func TestForegroundSubagentRunnerRunFailureSetsStatusFailed(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name: "planner",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyInherit,
		},
	})

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Err: errors.New("stream failed")},
		}},
	}
	runner := NewForegroundSubagentRunner(RunnerDeps{
		Market:         market,
		Store:          store,
		Provider:       provider,
		ParentRegistry: mockToolRegistry{},
	})

	_, err := runner.Run(context.Background(), ForegroundRunRequest{
		SubagentType: "planner",
		Prompt:       "run and fail",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stream failed") {
		t.Fatalf("Run() error = %v, want stream failed", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(store.List()) = %d, want 1", len(list))
	}
	if list[0].Status != StatusFailed {
		t.Fatalf("record.Status = %q, want %q", list[0].Status, StatusFailed)
	}
}

func TestForegroundSubagentRunnerRunAllowlistBlocksDisallowedTool(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name: "planner",
		ToolPolicy: ToolPolicy{
			Mode:      ToolPolicyAllowlist,
			Allowlist: []string{"shell"},
		},
	})

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{ToolCall: &types.ToolCall{ID: "call-1", Name: "think"}},
			{Done: true},
		}},
	}
	runner := NewForegroundSubagentRunner(RunnerDeps{
		Market:   market,
		Store:    store,
		Provider: provider,
		ParentRegistry: mockToolRegistry{
			definitions: []llm.ToolDefinition{{Name: "shell"}, {Name: "think"}},
			executors: map[string]toolExecutorFunc{
				"shell": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
					return types.ToolResult{}, nil
				},
				"think": func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
					return types.ToolResult{}, nil
				},
			},
		},
	})

	ret, err := runner.Run(context.Background(), ForegroundRunRequest{
		SubagentType: "planner",
		Prompt:       "call disallowed tool",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run() return type = %T, want map[string]any", ret.Value)
	}
	if got, _ := payload["status"].(string); got != string(StatusIdle) {
		t.Fatalf("Run() status = %q, want %q", got, StatusIdle)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "shell" {
		t.Fatalf("request tools = %#v, want one shell tool", requests[0].Tools)
	}
	if len(requests[1].Messages) < 3 {
		t.Fatalf("second request message count = %d, want >= 3", len(requests[1].Messages))
	}
	toolMessage := requests[1].Messages[len(requests[1].Messages)-1]
	if toolMessage.Role != "tool" {
		t.Fatalf("second request last role = %q, want tool", toolMessage.Role)
	}
	if !strings.Contains(contentPartsText(toolMessage.Content), "tool executor not found") {
		t.Fatalf("second request tool message = %#v, want contains tool executor not found", toolMessage.Content)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(store.List()) = %d, want 1", len(list))
	}
	if list[0].Status != StatusIdle {
		t.Fatalf("record.Status = %q, want %q", list[0].Status, StatusIdle)
	}
}

func TestForegroundSubagentRunnerValidationErrors(t *testing.T) {
	t.Parallel()

	runner := NewForegroundSubagentRunner(RunnerDeps{})
	if _, err := runner.Run(context.Background(), ForegroundRunRequest{Prompt: "x"}); err == nil {
		t.Fatal("Run(nil deps) error = nil, want error")
	}

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{Name: "planner"})
	runner = NewForegroundSubagentRunner(RunnerDeps{
		Market:         market,
		Store:          store,
		Provider:       &scriptedChatProvider{},
		ParentRegistry: mockToolRegistry{},
	})
	if _, err := runner.Run(context.Background(), ForegroundRunRequest{SubagentType: "planner", Prompt: "  "}); err == nil {
		t.Fatal("Run(empty prompt) error = nil, want error")
	}
}

func recordPathFor(store *SubagentStore, agentID string) string {
	path, err := store.metaPath(agentID)
	if err != nil {
		return ""
	}
	return path
}
