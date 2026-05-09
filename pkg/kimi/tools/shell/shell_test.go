package shell

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	corebg "github.com/wanpengxie/go-kimi/pkg/kimi/background"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
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

func TestShellExecuteSupportsCommandCompositions(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	tests := []struct {
		name     string
		command  string
		contains string
		notError bool
	}{
		{
			name:     "and chain",
			command:  "printf 'a' && printf 'b'",
			contains: "ab",
			notError: true,
		},
		{
			name:     "sequence",
			command:  "printf 'first'; printf ' second'",
			contains: "first second",
			notError: true,
		},
		{
			name:     "logical or",
			command:  "false || printf 'fallback'",
			contains: "fallback",
			notError: true,
		},
		{
			name:     "pipe",
			command:  "printf 'alpha\\nbeta\\n' | grep beta",
			contains: "beta",
			notError: true,
		},
		{
			name:     "multi pipe",
			command:  "printf 'alpha\\nbeta\\n' | grep beta | wc -l",
			contains: "1",
			notError: true,
		},
		{
			name:     "command substitution",
			command:  "printf \"value-$(printf 42)\"",
			contains: "value-42",
			notError: true,
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := tool.Execute(context.Background(), mustParams(t, executeParams{
				Command: tc.command,
				Timeout: 3,
			}))
			if err != nil {
				t.Fatalf("Execute(%s) error = %v", tc.name, err)
			}
			if tc.notError && result.IsError {
				t.Fatalf("result.IsError(%s) = %v, want false", tc.name, result.IsError)
			}
			if got := resultOutputText(t, result); !strings.Contains(got, tc.contains) {
				t.Fatalf("result output(%s) = %q, want contains %q", tc.name, got, tc.contains)
			}
		})
	}
}

func TestShellExecuteSupportsEnvironmentVariables(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	result, err := tool.Execute(context.Background(), mustParams(t, executeParams{
		Command: "GREETING=hello; printf \"$GREETING world\"",
		Timeout: 3,
	}))
	if err != nil {
		t.Fatalf("Execute(env vars) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "hello world" {
		t.Fatalf("result output = %q, want %q", got, "hello world")
	}
}

func TestShellExecuteSupportsFileOperations(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := New(workDir, nil)
	target := filepath.Join(workDir, "build.txt")

	result, err := tool.Execute(context.Background(), mustParams(t, executeParams{
		Command: "printf 'build artifact' > build.txt; cat build.txt",
		Timeout: 3,
	}))
	if err != nil {
		t.Fatalf("Execute(file operations) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "build artifact" {
		t.Fatalf("result output = %q, want %q", got, "build artifact")
	}

	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("os.ReadFile(build.txt) error = %v", readErr)
	}
	if got := string(content); got != "build artifact" {
		t.Fatalf("file content = %q, want %q", got, "build artifact")
	}
}

func TestShellExecuteShortSleepWithinTimeout(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	result, err := tool.Execute(context.Background(), mustParams(t, executeParams{
		Command: "sleep 0.2; printf done",
		Timeout: 1,
	}))
	if err != nil {
		t.Fatalf("Execute(short sleep) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "done" {
		t.Fatalf("result output = %q, want %q", got, "done")
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

func TestShellExecuteTruncatesLongLine(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	longLine := strings.Repeat("x", tools.MaxLineLengthChars+400)
	params := mustParams(t, executeParams{
		Command: "printf '" + longLine + "'",
		Timeout: 2,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, "...[line-truncated]") {
		t.Fatalf("result output = %q, want contains line-truncated suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxLineLengthChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxLineLengthChars)
	}
}

func TestShellExecuteTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "for i in $(seq 1 4000); do printf 'abcdefghijklmnop\\n'; done",
		Timeout: 2,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, "...[truncated]") {
		t.Fatalf("result output = %q, want contains truncated suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxOutputChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxOutputChars)
	}
}

func TestShellExecuteTruncatesLongOutputOnFailure(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "for i in $(seq 1 4000); do printf 'failure-line-%04d\\n' \"$i\"; done; exit 1",
		Timeout: 3,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute(failure truncation) error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, "...[truncated]") {
		t.Fatalf("result output = %q, want contains truncated suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxOutputChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxOutputChars)
	}
}

func TestShellExecuteContextCancellationKillsProcess(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := New(workDir, nil)
	target := filepath.Join(workDir, "should-not-exist.txt")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	result, err := tool.Execute(ctx, mustParams(t, executeParams{
		Command: "sleep 5; printf done > should-not-exist.txt",
		Timeout: 10,
	}))
	if err != nil {
		t.Fatalf("Execute(cancel) error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected %q to be absent after cancellation, stat error = %v", target, statErr)
	}
}

func TestShellExecuteNonZeroExitWithoutOutputIncludesExitStatus(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)
	params := mustParams(t, executeParams{
		Command: "exit 7",
		Timeout: 2,
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "exit status 7") {
		t.Fatalf("result output = %q, want contains exit status 7", resultOutputText(t, result))
	}
}

func TestShellExecuteTimeoutValidation(t *testing.T) {
	t.Parallel()

	tool := New(t.TempDir(), nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"printf ok"}`))
	if err != nil {
		t.Fatalf("Execute(default timeout) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError(default timeout) = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "ok" {
		t.Fatalf("result output(default timeout) = %q, want %q", got, "ok")
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"printf ok","timeout":601}`)); err == nil {
		t.Fatal("Execute(timeout=601) error = nil, want validation error")
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
