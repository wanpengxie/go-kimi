package shell

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestShellExecuteSuccess(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "printf 'hello world'",
		Timeout: 2,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "hello world") {
		t.Fatalf("result output = %q, want contains hello world", resultOutputText(t, result))
	}
}

func TestShellExecuteTimeout(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "sleep 2",
		Timeout: 1,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "timed out") {
		t.Fatalf("result output = %q, want contains timed out", resultOutputText(t, result))
	}
}

func TestShellExecuteApprovalRejected(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	targetFile := filepath.Join(workDir, "blocked.txt")
	command := "echo blocked > " + shellQuote(targetFile)

	var calledAction string
	var calledDesc string
	tool := New(workDir, func(_ context.Context, action, desc string) (bool, string) {
		calledAction = action
		calledDesc = desc
		return false, "blocked by policy"
	})
	params := mustParams(t, executeParams{
		Command: command,
		Timeout: 5,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if calledAction != "shell" {
		t.Fatalf("approver action = %q, want shell", calledAction)
	}
	if calledDesc != command {
		t.Fatalf("approver desc = %q, want %q", calledDesc, command)
	}
	if !strings.Contains(resultOutputText(t, result), "blocked by policy") {
		t.Fatalf("result output = %q, want contains blocked by policy", resultOutputText(t, result))
	}

	if _, statErr := os.Stat(targetFile); !os.IsNotExist(statErr) {
		t.Fatalf("expected %q to be absent, stat error = %v", targetFile, statErr)
	}
}

func TestShellExecuteNonZeroExitIsError(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "echo boom 1>&2; exit 3",
		Timeout: 2,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "boom") {
		t.Fatalf("result output = %q, want contains boom", resultOutputText(t, result))
	}
}

func TestShellExecuteBackgroundSuccess(t *testing.T) {
	t.Parallel()

	manager := &fakeBackgroundManager{taskID: "task-shell-1"}
	tool := NewWithBackground(t.TempDir(), nil, manager, "session-42")
	params := mustParams(t, executeParams{
		Command:    "echo background",
		Timeout:    12,
		Background: true,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute(background) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if len(manager.calls) != 1 {
		t.Fatalf("CreateBashTask call count = %d, want 1", len(manager.calls))
	}
	if got := manager.calls[0].Command; got != "echo background" {
		t.Fatalf("spec.Command = %q, want %q", got, "echo background")
	}
	if got := manager.calls[0].SessionID; got != "session-42" {
		t.Fatalf("spec.SessionID = %q, want %q", got, "session-42")
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["task_id"].(string); got != "task-shell-1" {
		t.Fatalf("payload.task_id = %q, want %q", got, "task-shell-1")
	}
	if got, _ := payload["run_in_background"].(bool); !got {
		t.Fatalf("payload.run_in_background = %v, want true", got)
	}
}

func TestShellExecuteBackgroundManagerErrors(t *testing.T) {
	t.Parallel()

	tool := NewWithBackground(t.TempDir(), nil, nil, "session-42")
	result, err := tool.Execute(context.Background(), mustParams(t, executeParams{
		Command:    "echo x",
		Timeout:    2,
		Background: true,
	}))
	if err != nil {
		t.Fatalf("Execute(background missing manager) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}

	tool = NewWithBackground(t.TempDir(), nil, &fakeBackgroundManager{err: errors.New("create failed")}, "session-42")
	result, err = tool.Execute(context.Background(), mustParams(t, executeParams{
		Command:    "echo x",
		Timeout:    2,
		Background: true,
	}))
	if err != nil {
		t.Fatalf("Execute(background manager error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "create failed") {
		t.Fatalf("result output = %q, want contains create failed", resultOutputText(t, result))
	}
}

func TestShellExecuteRejectsUnexpectedField(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo ok","timeout":1,"unexpected":true}`)); err == nil {
		t.Fatal("Execute(unexpected field) error = nil, want validation error")
	}
}

func mustParams(t *testing.T, input executeParams) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal(params) error = %v", err)
	}
	return data
}

func shellQuote(path string) string {
	path = strings.ReplaceAll(path, `'`, `'"'"'`)
	return "'" + path + "'"
}

func resultOutputText(t *testing.T, result types.ToolResult) string {
	t.Helper()
	output, ok := result.Value.Value.(string)
	if !ok {
		t.Fatalf("result output type = %T, want string", result.Value.Value)
	}
	return output
}

type fakeBackgroundManager struct {
	calls  []corebg.TaskSpec
	taskID string
	err    error
}

func (m *fakeBackgroundManager) CreateBashTask(_ context.Context, spec corebg.TaskSpec) (string, error) {
	m.calls = append(m.calls, spec)
	if m.err != nil {
		return "", m.err
	}
	if strings.TrimSpace(m.taskID) == "" {
		return "task-generated", nil
	}
	return m.taskID, nil
}
