package think

import (
	"context"
	"encoding/json"
	"testing"
)

func TestThinkExecuteReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	tool := New()
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"thought":"reasoning text"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if result.Name != "think" {
		t.Fatalf("result.Name = %q, want think", result.Name)
	}
	output, ok := result.Value.Value.(string)
	if !ok {
		t.Fatalf("result.Value.Value type = %T, want string", result.Value.Value)
	}
	if output != "" {
		t.Fatalf("result output = %q, want empty string", output)
	}
}

func TestThinkExecuteRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	tool := New()
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"thought":`))
	if err == nil {
		t.Fatal("Execute() error = nil, want decode error")
	}
}
