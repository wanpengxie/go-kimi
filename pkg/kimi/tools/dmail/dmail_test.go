package dmail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestSendDMailExecuteSuccess(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	if err := ctxStore.Append(soul.Message{
		Role:    soul.RoleUser,
		Content: types.ContentParts{types.TextPart{Text: "first"}},
	}); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	checkpointID := ctxStore.Checkpoint()
	if err := ctxStore.Append(soul.Message{
		Role:    soul.RoleAssistant,
		Content: types.ContentParts{types.TextPart{Text: "second"}},
	}); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}

	tool := New(ctxStore)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"checkpoint_id": checkpointID,
		"message":       "mail follow-up",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	messages := ctxStore.Messages()
	if len(messages) != checkpointID+1 {
		t.Fatalf("message count = %d, want %d", len(messages), checkpointID+1)
	}
	last := messages[len(messages)-1]
	if last.Role != soul.RoleUser {
		t.Fatalf("last role = %q, want %q", last.Role, soul.RoleUser)
	}
	if got := textFromParts(last.Content); got != "mail follow-up" {
		t.Fatalf("last message text = %q, want %q", got, "mail follow-up")
	}
}

func TestSendDMailExecuteRejectsInvalidParams(t *testing.T) {
	t.Parallel()

	tool := New(&stubMailContext{})
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"message": "only message",
	})); err == nil {
		t.Fatal("Execute(missing checkpoint_id) error = nil, want validation error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"checkpoint_id":1,"message":"ok","unexpected":true}`)); err == nil {
		t.Fatal("Execute(unexpected field) error = nil, want validation error")
	}
}

func TestSendDMailExecuteReturnsErrorResultOnRevertFailure(t *testing.T) {
	t.Parallel()

	tool := New(&stubMailContext{
		revertErr: errors.New("invalid checkpoint"),
	})
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"checkpoint_id": 10,
		"message":       "mail",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if got, _ := result.Value.Value.(string); !strings.Contains(got, "revert checkpoint") {
		t.Fatalf("error output = %q, want contains revert checkpoint", got)
	}
}

func TestSendDMailExecuteWithoutContext(t *testing.T) {
	t.Parallel()

	tool := New(nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"checkpoint_id": 0,
		"message":       "mail",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
}

type stubMailContext struct {
	revertErr error
	appendErr error
}

func (s *stubMailContext) RevertTo(_ int) error {
	if s == nil {
		return nil
	}
	return s.revertErr
}

func (s *stubMailContext) Append(_ soul.Message) error {
	if s == nil {
		return nil
	}
	return s.appendErr
}

func mustParams(t *testing.T, input any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func textFromParts(parts types.ContentParts) string {
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			return typed.Text
		case *types.TextPart:
			if typed != nil {
				return typed.Text
			}
		}
	}
	return ""
}
