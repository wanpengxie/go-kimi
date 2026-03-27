package background

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestBackgroundTaskManagerCreateBashTaskCompleted(t *testing.T) {
	t.Parallel()

	manager, _ := newManagerForTest(t, nil)

	taskID, err := manager.CreateBashTask(context.Background(), TaskSpec{
		SessionID:   "session-1",
		Description: "echo output",
		Command:     `printf "hello"; printf " world" >&2`,
		TimeoutSec:  5,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	view := waitForTerminalTask(t, manager, taskID, 5*time.Second)
	if view.Runtime.Status != TaskCompleted {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, TaskCompleted)
	}
	if view.Runtime.ExitCode == nil || *view.Runtime.ExitCode != 0 {
		t.Fatalf("task exit code = %v, want 0", view.Runtime.ExitCode)
	}
	if view.Runtime.TimedOut {
		t.Fatal("task timed_out = true, want false")
	}

	output, err := manager.ReadOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput() error = %v", err)
	}
	outputText := string(output)
	if !strings.Contains(outputText, "hello") || !strings.Contains(outputText, "world") {
		t.Fatalf("output = %q, want contains hello/world", outputText)
	}

	list, err := manager.ListTasks(10)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(list) == 0 {
		t.Fatal("ListTasks() returned empty list")
	}
}

func TestBackgroundTaskManagerCreateBashTaskTimeout(t *testing.T) {
	t.Parallel()

	manager, _ := newManagerForTest(t, nil)

	taskID, err := manager.CreateBashTask(context.Background(), TaskSpec{
		Command:    "sleep 2",
		TimeoutSec: 1,
	})
	if err != nil {
		t.Fatalf("CreateBashTask(timeout) error = %v", err)
	}

	view := waitForTerminalTask(t, manager, taskID, 5*time.Second)
	if view.Runtime.Status != TaskFailed {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, TaskFailed)
	}
	if !view.Runtime.TimedOut {
		t.Fatal("task timed_out = false, want true")
	}
	if !strings.Contains(view.Runtime.FailureReason, "timed out") {
		t.Fatalf("task failure_reason = %q, want contains timed out", view.Runtime.FailureReason)
	}
}

func TestBackgroundTaskManagerKillBashTask(t *testing.T) {
	t.Parallel()

	manager, _ := newManagerForTest(t, nil)

	taskID, err := manager.CreateBashTask(context.Background(), TaskSpec{
		Command:    "sleep 10",
		TimeoutSec: 0,
	})
	if err != nil {
		t.Fatalf("CreateBashTask(kill) error = %v", err)
	}

	_ = waitForTaskStatus(t, manager, taskID, 3*time.Second, func(status TaskStatus) bool {
		return status == TaskStarting || status == TaskRunning
	})

	if err := manager.KillTask(taskID, "manual stop"); err != nil {
		t.Fatalf("KillTask() error = %v", err)
	}

	view := waitForTerminalTask(t, manager, taskID, 5*time.Second)
	if view.Runtime.Status != TaskKilled {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, TaskKilled)
	}
	if view.Control.KillRequestedAt == nil {
		t.Fatal("control.kill_requested_at = nil, want non-nil")
	}
	if view.Control.KillReason != "manual stop" {
		t.Fatalf("control.kill_reason = %q, want %q", view.Control.KillReason, "manual stop")
	}
}

func TestBackgroundTaskManagerCreateAgentTask(t *testing.T) {
	t.Parallel()

	runner := &fakeSubagentRunner{
		run: func(_ context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error) {
			return types.ToolReturnValue{
				Value: map[string]any{
					"agent_id":      req.AgentID,
					"subagent_type": req.SubagentType,
					"output_text":   "agent output",
				},
			}, nil
		},
	}
	manager, _ := newManagerForTest(t, runner)

	taskID, err := manager.CreateAgentTask(context.Background(), TaskSpec{
		AgentID:       "agent-1",
		SubagentType:  "planner",
		Prompt:        "Plan work",
		ModelOverride: "kimi-k2.5",
		TimeoutSec:    3,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}

	view := waitForTerminalTask(t, manager, taskID, 5*time.Second)
	if view.Runtime.Status != TaskCompleted {
		t.Fatalf("task status = %q, want %q", view.Runtime.Status, TaskCompleted)
	}

	output, err := manager.ReadOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput(agent) error = %v", err)
	}
	if !strings.Contains(string(output), "agent output") {
		t.Fatalf("agent output = %q, want contains %q", string(output), "agent output")
	}

	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("runner call count = %d, want 1", len(calls))
	}
	if calls[0].Prompt != "Plan work" {
		t.Fatalf("runner prompt = %q, want %q", calls[0].Prompt, "Plan work")
	}
}

func TestBackgroundTaskManagerShutdownAndValidation(t *testing.T) {
	t.Parallel()

	var nilManager *BackgroundTaskManager
	if _, err := nilManager.CreateBashTask(context.Background(), TaskSpec{Command: "echo"}); err == nil {
		t.Fatal("nil manager CreateBashTask() error = nil, want error")
	}

	managerWithoutStore := NewBackgroundTaskManager(ManagerDeps{})
	if _, err := managerWithoutStore.CreateBashTask(context.Background(), TaskSpec{Command: "echo"}); err == nil {
		t.Fatal("CreateBashTask(nil store) error = nil, want error")
	}

	manager, _ := newManagerForTest(t, nil)
	taskID, err := manager.CreateBashTask(context.Background(), TaskSpec{
		Command:    "sleep 10",
		TimeoutSec: 0,
	})
	if err != nil {
		t.Fatalf("CreateBashTask(before shutdown) error = %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	view := waitForTerminalTask(t, manager, taskID, 5*time.Second)
	if view.Runtime.Status != TaskKilled {
		t.Fatalf("task status after shutdown = %q, want %q", view.Runtime.Status, TaskKilled)
	}

	if _, err := manager.CreateBashTask(context.Background(), TaskSpec{Command: "echo hi"}); err == nil {
		t.Fatal("CreateBashTask(after shutdown) error = nil, want error")
	}
}

func newManagerForTest(t *testing.T, runner SubagentRunner) (*BackgroundTaskManager, *BackgroundTaskStore) {
	t.Helper()

	store := NewBackgroundTaskStore(filepath.Join(t.TempDir(), "tasks"))
	manager := NewBackgroundTaskManager(ManagerDeps{
		Store:          store,
		SubagentRunner: runner,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return manager, store
}

func waitForTaskStatus(
	t *testing.T,
	manager *BackgroundTaskManager,
	taskID string,
	timeout time.Duration,
	predicate func(status TaskStatus) bool,
) *TaskView {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastView *TaskView
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

func waitForTerminalTask(t *testing.T, manager *BackgroundTaskManager, taskID string, timeout time.Duration) *TaskView {
	t.Helper()
	return waitForTaskStatus(t, manager, taskID, timeout, func(status TaskStatus) bool {
		return status.IsTerminal()
	})
}

type fakeSubagentRunner struct {
	mu    sync.Mutex
	calls []subagents.ForegroundRunRequest
	run   func(ctx context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error)
}

func (r *fakeSubagentRunner) Run(ctx context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	run := r.run
	r.mu.Unlock()

	if run == nil {
		return types.ToolReturnValue{}, nil
	}
	return run(ctx, req)
}

func (r *fakeSubagentRunner) Calls() []subagents.ForegroundRunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]subagents.ForegroundRunRequest, len(r.calls))
	copy(out, r.calls)
	return out
}
