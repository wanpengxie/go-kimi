//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/session"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestScriptedSessionLifecycle(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}
	if !s.IsEmpty() {
		t.Fatal("new session should be empty")
	}

	found, err := session.Find(workDir, s.ID)
	if err != nil {
		t.Fatalf("session.Find() error = %v", err)
	}
	if found.ID != s.ID {
		t.Fatalf("session.Find().ID = %q, want %q", found.ID, s.ID)
	}

	listed, err := session.List(workDir)
	if err != nil {
		t.Fatalf("session.List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != s.ID {
		t.Fatalf("session.List() = %#v, want one session %q", listed, s.ID)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	if continued.ID != s.ID {
		t.Fatalf("session.Continue().ID = %q, want %q", continued.ID, s.ID)
	}

	ctxStore := soul.NewSoulContext(continued.Dir)
	if err := ctxStore.Append(soul.Message{
		Role: soul.RoleUser,
		Content: types.ContentParts{
			types.TextPart{Text: "scripted-session-lifecycle"},
		},
	}); err != nil {
		t.Fatalf("SoulContext.Append() error = %v", err)
	}

	continuedAgain, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue(second) error = %v", err)
	}
	if continuedAgain.IsEmpty() {
		t.Fatal("session should not be empty after context append")
	}

	if err := continuedAgain.Delete(); err != nil {
		t.Fatalf("Session.Delete() error = %v", err)
	}
	if _, err := session.Find(workDir, s.ID); err == nil {
		t.Fatal("session.Find() after delete error = nil, want error")
	}

	listed, err = session.List(workDir)
	if err != nil {
		t.Fatalf("session.List() after delete error = %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("len(session.List()) after delete = %d, want 0", len(listed))
	}
	if _, err := session.Continue(workDir); err == nil {
		t.Fatal("session.Continue() after delete error = nil, want error")
	}
}

func TestScriptedForegroundSubagent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	const token = "SCRIPTED_FOREGROUND_SUBAGENT_TOKEN_2026"
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: token}},
				{Done: true},
			},
		},
	}

	market := subagents.NewLaborMarket()
	market.Register(&subagents.AgentTypeDefinition{
		Name: "general-purpose",
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

	ret, err := runner.Run(context.Background(), subagents.ForegroundRunRequest{
		Prompt: "reply with token",
	})
	if err != nil {
		t.Fatalf("ForegroundSubagentRunner.Run() error = %v", err)
	}

	payload, ok := ret.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run() payload type = %T, want map[string]any", ret.Value)
	}
	agentID, _ := payload["agent_id"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("Run() agent_id = %q, want non-empty", agentID)
	}
	if got, _ := payload["subagent_type"].(string); got != "general-purpose" {
		t.Fatalf("Run() subagent_type = %q, want general-purpose", got)
	}
	if got, _ := payload["output_text"].(string); !strings.Contains(got, token) {
		t.Fatalf("Run() output_text = %q, want contains %q", got, token)
	}

	record, err := store.Get(agentID)
	if err != nil {
		t.Fatalf("SubagentStore.Get(%q) error = %v", agentID, err)
	}
	if record.Status != subagents.StatusIdle {
		t.Fatalf("record.Status = %q, want %q", record.Status, subagents.StatusIdle)
	}

	contextPayload, err := os.ReadFile(filepath.Join(s.SubagentsDir(), agentID, "context.jsonl"))
	if err != nil {
		t.Fatalf("os.ReadFile(context.jsonl) error = %v", err)
	}
	if !strings.Contains(string(contextPayload), token) {
		t.Fatalf("context.jsonl = %q, want contains %q", string(contextPayload), token)
	}
}

func TestScriptedBackgroundBashTask(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	manager := newE2EBackgroundManager(t, s.TasksDir(), nil)
	const token = "SCRIPTED_BACKGROUND_BASH_TOKEN_2026"
	taskID, err := manager.CreateBashTask(context.Background(), corebg.TaskSpec{
		SessionID:   s.ID,
		Description: "scripted background bash",
		Command:     "printf " + token,
		TimeoutSec:  5,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	view := waitForTaskViewStatus(t, manager, taskID, 5*time.Second, func(status corebg.TaskStatus) bool {
		return status.IsTerminal()
	})
	if view.Runtime.Status != corebg.TaskCompleted {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, corebg.TaskCompleted)
	}
	if view.Spec.SessionID != s.ID {
		t.Fatalf("task session_id = %q, want %q", view.Spec.SessionID, s.ID)
	}

	output, err := manager.ReadOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput() error = %v", err)
	}
	if !strings.Contains(string(output), token) {
		t.Fatalf("task output = %q, want contains %q", string(output), token)
	}
}

func TestScriptedBackgroundAgentTask(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	const token = "SCRIPTED_BACKGROUND_AGENT_TOKEN_2026"
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: token}},
				{Done: true},
			},
		},
	}
	market := subagents.NewLaborMarket()
	market.Register(&subagents.AgentTypeDefinition{
		Name: "general-purpose",
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

	manager := newE2EBackgroundManager(t, s.TasksDir(), runner)
	taskID, err := manager.CreateAgentTask(context.Background(), corebg.TaskSpec{
		SessionID:    s.ID,
		Description:  "scripted background agent",
		SubagentType: "general-purpose",
		Prompt:       "reply with token",
		TimeoutSec:   5,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}

	view := waitForTaskViewStatus(t, manager, taskID, 5*time.Second, func(status corebg.TaskStatus) bool {
		return status.IsTerminal()
	})
	if view.Runtime.Status != corebg.TaskCompleted {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, corebg.TaskCompleted)
	}
	if view.Spec.Kind != corebg.TaskKindAgent {
		t.Fatalf("task kind = %q, want %q", view.Spec.Kind, corebg.TaskKindAgent)
	}

	output, err := manager.ReadOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput() error = %v", err)
	}
	if !strings.Contains(string(output), token) {
		t.Fatalf("task output = %q, want contains %q", string(output), token)
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

func TestScriptedTaskKill(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	manager := newE2EBackgroundManager(t, s.TasksDir(), nil)
	taskID, err := manager.CreateBashTask(context.Background(), corebg.TaskSpec{
		SessionID:   s.ID,
		Description: "scripted kill task",
		Command:     "sleep 30",
		TimeoutSec:  0,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	_ = waitForTaskViewStatus(t, manager, taskID, 3*time.Second, func(status corebg.TaskStatus) bool {
		return status == corebg.TaskStarting || status == corebg.TaskRunning
	})

	const killReason = "scripted kill request"
	if err := manager.KillTask(taskID, killReason); err != nil {
		t.Fatalf("KillTask() error = %v", err)
	}

	view := waitForTaskViewStatus(t, manager, taskID, 5*time.Second, func(status corebg.TaskStatus) bool {
		return status.IsTerminal()
	})
	if view.Runtime.Status != corebg.TaskKilled {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, corebg.TaskKilled)
	}
	if view.Control.KillReason != killReason {
		t.Fatalf("task kill_reason = %q, want %q", view.Control.KillReason, killReason)
	}
	if !strings.Contains(view.Runtime.FailureReason, killReason) {
		t.Fatalf("task failure_reason = %q, want contains %q", view.Runtime.FailureReason, killReason)
	}
}

func newE2EBackgroundManager(t *testing.T, tasksDir string, runner corebg.SubagentRunner) *corebg.BackgroundTaskManager {
	t.Helper()

	manager := corebg.NewBackgroundTaskManager(corebg.ManagerDeps{
		Store:          corebg.NewBackgroundTaskStore(tasksDir),
		SubagentRunner: runner,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return manager
}

func waitForTaskViewStatus(
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
		time.Sleep(20 * time.Millisecond)
	}

	if lastView == nil {
		t.Fatalf("task %q status wait timed out with no view", taskID)
	}
	t.Fatalf("task %q status wait timed out, last status=%q", taskID, lastView.Runtime.Status)
	return nil
}
