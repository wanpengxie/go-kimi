//go:build e2e_live

package e2e

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm/moonshot"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/session"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	agenttool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/agent"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveSessionWithSoul(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	ctxStore := soul.NewSoulContext(s.Dir)
	if err := ctxStore.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore() error = %v", err)
	}
	engine := soul.NewSoul(provider, ctxStore, nil, liveWireFileEmitter{file: wire.NewWireFile(s.WireFile)}, "")

	const token = "LIVE_SESSION_SOUL_TOKEN_2026"
	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Reply with this token only: " + token},
	})
	if err != nil {
		t.Fatalf("Soul.Run() error = %v", err)
	}
	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}

	contextPayload, err := os.ReadFile(s.ContextFile)
	if err != nil {
		t.Fatalf("os.ReadFile(context) error = %v", err)
	}
	if strings.TrimSpace(string(contextPayload)) == "" {
		t.Fatal("context.jsonl should not be empty after soul run")
	}

	wirePayload, err := os.ReadFile(s.WireFile)
	if err != nil {
		t.Fatalf("os.ReadFile(wire) error = %v", err)
	}
	if strings.TrimSpace(string(wirePayload)) == "" {
		t.Fatal("wire.jsonl should not be empty after soul run")
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	if continued.ID != s.ID {
		t.Fatalf("session.Continue().ID = %q, want %q", continued.ID, s.ID)
	}

	restored := soul.NewSoulContext(continued.Dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore(continued) error = %v", err)
	}
	messages := restored.Messages()
	if len(messages) < 2 {
		t.Fatalf("restored messages = %d, want >= 2", len(messages))
	}
	if messages[0].Role != soul.RoleUser {
		t.Fatalf("restored first role = %q, want user", messages[0].Role)
	}
	if messages[len(messages)-1].Role != soul.RoleAssistant {
		t.Fatalf("restored last role = %q, want assistant", messages[len(messages)-1].Role)
	}
}

func TestLiveForegroundSubagent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	market := subagents.NewLaborMarket()
	market.Register(&subagents.AgentTypeDefinition{
		Name:               "general-purpose",
		SupportsBackground: true,
		ToolPolicy: subagents.ToolPolicy{
			Mode: subagents.ToolPolicyInherit,
		},
	})
	store := subagents.NewSubagentStore(s.SubagentsDir())
	runner := subagents.NewForegroundSubagentRunner(subagents.RunnerDeps{
		Market:   market,
		Store:    store,
		Provider: provider,
		WorkDir:  workDir,
	})

	const token = "LIVE_FOREGROUND_SUBAGENT_TOKEN_2026"
	ret, err := runner.Run(ctx, subagents.ForegroundRunRequest{
		SubagentType: "general-purpose",
		Prompt:       "Reply with EXACT token " + token + " only.",
	})
	if err != nil {
		t.Fatalf("ForegroundSubagentRunner.Run() error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run() payload type = %T, want map[string]any", ret.Value)
	}
	if got, _ := payload["subagent_type"].(string); got != "general-purpose" {
		t.Fatalf("Run() subagent_type = %q, want general-purpose", got)
	}
	if got, _ := payload["output_text"].(string); !containsCaseFold(got, token) {
		t.Fatalf("Run() output_text = %q, want contains %q", got, token)
	}

	records, err := store.List()
	if err != nil {
		t.Fatalf("SubagentStore.List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(SubagentStore.List()) = %d, want 1", len(records))
	}
	if records[0].Status != subagents.StatusIdle {
		t.Fatalf("subagent status = %q, want %q", records[0].Status, subagents.StatusIdle)
	}
}

func TestLiveBackgroundBashTask(t *testing.T) {
	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	manager := newLiveBackgroundManager(t, s.TasksDir(), nil)
	const token = "LIVE_BACKGROUND_BASH_TOKEN_2026"
	taskID, err := manager.CreateBashTask(context.Background(), corebg.TaskSpec{
		SessionID:   s.ID,
		Description: "live background bash",
		Command:     "printf " + token,
		TimeoutSec:  10,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	view := waitForLiveTaskViewStatus(t, manager, taskID, 10*time.Second, func(status corebg.TaskStatus) bool {
		return status.IsTerminal()
	})
	if view.Runtime.Status != corebg.TaskCompleted {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, corebg.TaskCompleted)
	}

	output, err := manager.ReadOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput() error = %v", err)
	}
	if !strings.Contains(string(output), token) {
		t.Fatalf("task output = %q, want contains %q", string(output), token)
	}
}

func TestLiveSoulWithAgentTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	market := subagents.NewLaborMarket()
	market.Register(&subagents.AgentTypeDefinition{
		Name:               "general-purpose",
		SupportsBackground: true,
		ToolPolicy: subagents.ToolPolicy{
			Mode: subagents.ToolPolicyInherit,
		},
	})
	store := subagents.NewSubagentStore(s.SubagentsDir())
	runner := subagents.NewForegroundSubagentRunner(subagents.RunnerDeps{
		Market:   market,
		Store:    store,
		Provider: provider,
		WorkDir:  workDir,
	})

	manager := newLiveBackgroundManager(t, s.TasksDir(), runner)
	agentTool := agenttool.New(runner, manager)
	agentTool.SessionID = s.ID
	agentTool.TimeoutSec = 30

	ctxStore := soul.NewSoulContext(s.Dir)
	if err := ctxStore.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore() error = %v", err)
	}
	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(agentTool),
		wire.NoopEmitter{},
		"You must call the agent tool exactly once before final answer when asked.",
	)

	const token = "LIVE_AGENT_TOOL_TOKEN_2026"
	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Call the agent tool exactly once in foreground. Send it a prompt that asks for exact token " + token + ". After tool returns, reply with token only."},
	})
	if err != nil {
		t.Fatalf("Soul.Run() with agent tool error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
	messages := ctxStore.Messages()
	if !contextHasToolCallName(messages, "agent") {
		t.Fatalf("context does not contain agent tool call: %#v", messages)
	}
	if !contextHasToolOutputContains(messages, token) {
		t.Fatalf("context does not contain token from agent tool output: %#v", messages)
	}

	records, err := store.List()
	if err != nil {
		t.Fatalf("SubagentStore.List() error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("SubagentStore.List() returned 0 records, want >=1")
	}
}

type liveWireFileEmitter struct {
	file *wire.WireFile
}

func (e liveWireFileEmitter) Emit(msg wire.WireMessage) error {
	if e.file == nil {
		return errors.New("live wire emitter: nil wire file")
	}
	return e.file.AppendMessage(msg)
}

func newM4LiveProvider(t *testing.T, ctx context.Context) *moonshot.MoonshotClient {
	t.Helper()

	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Fatal("KIMI_API_KEY must be set for e2e_live tests")
	}
	baseURL := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	resolvedModel, err := resolveLiveModel(ctx, apiKey, baseURL, os.Getenv("KIMI_MODEL"))
	if err != nil {
		t.Fatalf("resolve live model error = %v", err)
	}
	t.Logf("live model resolved from %s: %s", resolvedModel.Source, resolvedModel.Model)
	return moonshot.NewMoonshotClient(apiKey, baseURL, resolvedModel.Model)
}

func newLiveBackgroundManager(t *testing.T, tasksDir string, runner corebg.SubagentRunner) *corebg.BackgroundTaskManager {
	t.Helper()

	manager := corebg.NewBackgroundTaskManager(corebg.ManagerDeps{
		Store:          corebg.NewBackgroundTaskStore(tasksDir),
		SubagentRunner: runner,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return manager
}

func waitForLiveTaskViewStatus(
	t *testing.T,
	manager *corebg.BackgroundTaskManager,
	taskID string,
	timeout time.Duration,
	predicate func(status corebg.TaskStatus) bool,
) *corebg.TaskView {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastView *corebg.TaskView
	for time.Now().Before(deadline) {
		view, err := manager.GetTask(taskID)
		if err == nil {
			lastView = view
			if predicate(view.Runtime.Status) {
				return view
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if lastView == nil {
		t.Fatalf("task %q status wait timed out with no view", taskID)
	}
	t.Fatalf("task %q status wait timed out, last status=%q", taskID, lastView.Runtime.Status)
	return nil
}

func containsCaseFold(text, needle string) bool {
	return strings.Contains(strings.ToUpper(text), strings.ToUpper(needle))
}
