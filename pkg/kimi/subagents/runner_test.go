package subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if got, _ := payload["status"].(string); got != string(StatusCompleted) {
		t.Fatalf("Run() status = %q, want %q", got, StatusCompleted)
	}
	if got, _ := payload["output_text"].(string); got != "subagent output" {
		t.Fatalf("Run() output_text = %q, want %q", got, "subagent output")
	}

	record, err := store.Get(agentID)
	if err != nil {
		t.Fatalf("store.Get(%q) error = %v", agentID, err)
	}
	if record.Status != StatusCompleted {
		t.Fatalf("record.Status = %q, want %q", record.Status, StatusCompleted)
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
	if updated.Status != StatusCompleted {
		t.Fatalf("updated.Status = %q, want %q", updated.Status, StatusCompleted)
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
		streams: [][]llm.ChatEvent{
			{{Err: errors.New("stream failed")}},
			{{Err: errors.New("stream failed")}},
			{{Err: errors.New("stream failed")}},
			{{Err: errors.New("stream failed")}},
		},
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
	if got, _ := payload["status"].(string); got != string(StatusCompleted) {
		t.Fatalf("Run() status = %q, want %q", got, StatusCompleted)
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
	if list[0].Status != StatusCompleted {
		t.Fatalf("record.Status = %q, want %q", list[0].Status, StatusCompleted)
	}
}

func TestForegroundSubagentRunnerRunExistingRejectsRunningStatus(t *testing.T) {
	t.Parallel()

	statuses := []SubagentStatus{
		StatusRunningForeground,
		StatusRunningBackground,
	}
	for i := range statuses {
		status := statuses[i]
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
			record := &AgentInstanceRecord{
				AgentID:      "agent-running",
				SubagentType: "planner",
				Status:       status,
				Description:  "existing",
				CreatedAt:    10,
				UpdatedAt:    10,
				LaunchSpec: AgentLaunchSpec{
					CreatedAt: 10,
				},
			}
			if err := store.Create(record); err != nil {
				t.Fatalf("store.Create() error = %v", err)
			}

			market := NewLaborMarket()
			market.Register(&AgentTypeDefinition{
				Name: "planner",
				ToolPolicy: ToolPolicy{
					Mode: ToolPolicyInherit,
				},
			})
			runner := NewForegroundSubagentRunner(RunnerDeps{
				Market:         market,
				Store:          store,
				Provider:       &scriptedChatProvider{},
				ParentRegistry: mockToolRegistry{},
			})

			_, err := runner.Run(context.Background(), ForegroundRunRequest{
				AgentID: "agent-running",
				Prompt:  "continue running agent",
			})
			if err == nil {
				t.Fatal("Run(running status) error = nil, want error")
			}
			if !strings.Contains(err.Error(), "already running") {
				t.Fatalf("Run(running status) error = %v, want contains already running", err)
			}

			updated, err := store.Get("agent-running")
			if err != nil {
				t.Fatalf("store.Get() error = %v", err)
			}
			if updated.Status != status {
				t.Fatalf("updated.Status = %q, want %q", updated.Status, status)
			}
		})
	}
}

func TestForegroundSubagentRunnerRunBackgroundPersistsTaskAssociation(t *testing.T) {
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
			{Delta: types.TextPart{Text: "background output"}},
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
		SubagentType:     "planner",
		Prompt:           "run in background",
		Background:       true,
		BackgroundTaskID: "task-42",
	})
	if err != nil {
		t.Fatalf("Run(background) error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run(background) return type = %T, want map[string]any", ret.Value)
	}
	agentID, _ := payload["agent_id"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("Run(background) agent_id = %q, want non-empty", agentID)
	}
	if got, _ := payload["status"].(string); got != string(StatusCompleted) {
		t.Fatalf("Run(background) status = %q, want %q", got, StatusCompleted)
	}

	record, err := store.Get(agentID)
	if err != nil {
		t.Fatalf("store.Get(%q) error = %v", agentID, err)
	}
	if record.Status != StatusCompleted {
		t.Fatalf("record.Status = %q, want %q", record.Status, StatusCompleted)
	}
	if record.LastTaskID != "task-42" {
		t.Fatalf("record.LastTaskID = %q, want task-42", record.LastTaskID)
	}
}

func TestForegroundSubagentRunnerRunAppliesModelOverrideToProvider(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name:         "planner",
		DefaultModel: "kimi-k2",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyInherit,
		},
	})

	provider := newModelTrackingProvider("base-model")
	runner := NewForegroundSubagentRunner(RunnerDeps{
		Market:         market,
		Store:          store,
		Provider:       provider,
		ParentRegistry: mockToolRegistry{},
	})

	ret, err := runner.Run(context.Background(), ForegroundRunRequest{
		SubagentType:  "planner",
		Prompt:        "use override model",
		ModelOverride: "kimi-k2.5",
	})
	if err != nil {
		t.Fatalf("Run(model override) error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run(model override) return type = %T, want map[string]any", ret.Value)
	}
	agentID, _ := payload["agent_id"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("Run(model override) agent_id = %q, want non-empty", agentID)
	}

	usedModels := provider.UsedModels()
	if len(usedModels) != 1 {
		t.Fatalf("provider used model count = %d, want 1", len(usedModels))
	}
	if usedModels[0] != "kimi-k2.5" {
		t.Fatalf("provider used model = %q, want kimi-k2.5", usedModels[0])
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

type modelTrackingProvider struct {
	model  string
	shared *modelTrackingShared
}

type modelTrackingShared struct {
	mu         sync.Mutex
	usedModels []string
}

func newModelTrackingProvider(model string) *modelTrackingProvider {
	return &modelTrackingProvider{
		model: strings.TrimSpace(model),
		shared: &modelTrackingShared{
			usedModels: make([]string, 0, 1),
		},
	}
}

func (p *modelTrackingProvider) ModelName() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.model)
}

func (p *modelTrackingProvider) WithModel(model string) llm.ChatProvider {
	if p == nil {
		return p
	}
	clone := *p
	if normalized := strings.TrimSpace(model); normalized != "" {
		clone.model = normalized
	}
	return &clone
}

func (p *modelTrackingProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *modelTrackingProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *modelTrackingProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	if p != nil && p.shared != nil {
		p.shared.mu.Lock()
		p.shared.usedModels = append(p.shared.usedModels, strings.TrimSpace(p.model))
		p.shared.mu.Unlock()
	}
	ch := make(chan llm.ChatEvent, 1)
	ch <- llm.ChatEvent{Done: true}
	close(ch)
	return ch, nil
}

func (p *modelTrackingProvider) UsedModels() []string {
	if p == nil || p.shared == nil {
		return nil
	}
	p.shared.mu.Lock()
	defer p.shared.mu.Unlock()
	out := make([]string, len(p.shared.usedModels))
	copy(out, p.shared.usedModels)
	return out
}
