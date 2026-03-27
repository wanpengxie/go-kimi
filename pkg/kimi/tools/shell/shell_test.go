package shell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
