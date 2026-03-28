package soul

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestSoulContextAppendRestoreRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := NewSoulContext(dir)

	wantMessages := []Message{
		{
			Role: RoleSystem,
			Content: types.ContentParts{
				types.TextPart{Text: "summary"},
			},
		},
		{
			Role: RoleUser,
			Content: types.ContentParts{
				types.TextPart{Text: "hello"},
			},
		},
		{
			Role: RoleAssistant,
			Content: types.ContentParts{
				types.TextPart{Text: "hi"},
			},
			ToolCalls: []types.ToolCall{
				{
					ID:   "call-1",
					Name: "search",
					Arguments: map[string]any{
						"q": "go test",
					},
				},
			},
		},
		{
			Role: RoleTool,
			Content: types.ContentParts{
				types.TextPart{Text: "tool result"},
			},
			ToolCallID: "call-1",
		},
	}

	for i := range wantMessages {
		if err := ctx.Append(wantMessages[i]); err != nil {
			t.Fatalf("Append[%d]() error = %v", i, err)
		}
	}
	ctx.UpdateTokenCount(1234)

	restored := NewSoulContext(dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	gotMessages := restored.Messages()
	if !reflect.DeepEqual(gotMessages, wantMessages) {
		t.Fatalf("restored messages mismatch\ngot = %#v\nwant = %#v", gotMessages, wantMessages)
	}
	if restored.TokenCount() != 1234 {
		t.Fatalf("TokenCount() = %d, want %d", restored.TokenCount(), 1234)
	}
}

func TestSoulContextClearArchivesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := NewSoulContext(dir)
	if err := ctx.Append(Message{
		Role: RoleUser,
		Content: types.ContentParts{
			types.TextPart{Text: "history"},
		},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	ctx.UpdateTokenCount(88)

	historyPath := filepath.Join(dir, contextJSONLFile)
	beforeClear, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("os.ReadFile(history before clear) error = %v", err)
	}

	if err := ctx.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	if got := ctx.Messages(); len(got) != 0 {
		t.Fatalf("Messages() length after Clear = %d, want 0", len(got))
	}
	if got := ctx.TokenCount(); got != 0 {
		t.Fatalf("TokenCount() after Clear = %d, want 0", got)
	}
	if _, err := os.Stat(historyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history file should be archived, stat err = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	var archivedPath string
	for i := range entries {
		name := entries[i].Name()
		if strings.HasPrefix(name, "context-") && strings.HasSuffix(name, ".jsonl") {
			archivedPath = filepath.Join(dir, name)
			break
		}
	}
	if archivedPath == "" {
		t.Fatal("archived context file not found")
	}

	archivedContent, err := os.ReadFile(archivedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(archived) error = %v", err)
	}
	if string(archivedContent) != string(beforeClear) {
		t.Fatalf("archived content mismatch\ngot = %q\nwant = %q", archivedContent, beforeClear)
	}

	restored := NewSoulContext(dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("Restore() after Clear error = %v", err)
	}
	if len(restored.Messages()) != 0 {
		t.Fatalf("restored Messages() length after Clear = %d, want 0", len(restored.Messages()))
	}
	if restored.TokenCount() != 0 {
		t.Fatalf("restored TokenCount() after Clear = %d, want 0", restored.TokenCount())
	}
}

func TestSoulContextTokenCountUpdatePersistsLatest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := NewSoulContext(dir)

	ctx.UpdateTokenCount(7)
	ctx.UpdateTokenCount(42)
	ctx.UpdateTokenCount(99)

	if got := ctx.TokenCount(); got != 99 {
		t.Fatalf("TokenCount() = %d, want %d", got, 99)
	}

	restored := NewSoulContext(dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := restored.TokenCount(); got != 99 {
		t.Fatalf("restored TokenCount() = %d, want %d", got, 99)
	}
	if len(restored.Messages()) != 0 {
		t.Fatalf("restored Messages() length = %d, want 0", len(restored.Messages()))
	}
}

func TestSoulContextReplaceRewritesHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := NewSoulContext(dir)
	if err := ctx.Append(Message{
		Role:    RoleUser,
		Content: types.ContentParts{types.TextPart{Text: "old user"}},
	}); err != nil {
		t.Fatalf("Append(old user) error = %v", err)
	}
	if err := ctx.Append(Message{
		Role:    RoleAssistant,
		Content: types.ContentParts{types.TextPart{Text: "old assistant"}},
	}); err != nil {
		t.Fatalf("Append(old assistant) error = %v", err)
	}
	ctx.UpdateTokenCount(10)

	replacedMessages := []Message{
		{
			Role:    RoleSystem,
			Content: types.ContentParts{types.TextPart{Text: "compressed summary"}},
		},
		{
			Role:    RoleUser,
			Content: types.ContentParts{types.TextPart{Text: "recent user"}},
		},
	}
	if err := ctx.Replace(replacedMessages, 77); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	restored := NewSoulContext(dir)
	if err := restored.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := restored.Messages(); !reflect.DeepEqual(got, replacedMessages) {
		t.Fatalf("restored messages mismatch\ngot = %#v\nwant = %#v", got, replacedMessages)
	}
	if got := restored.TokenCount(); got != 77 {
		t.Fatalf("restored TokenCount() = %d, want %d", got, 77)
	}
}

func TestSoulContextAppendValidation(t *testing.T) {
	t.Parallel()

	ctx := NewSoulContext(t.TempDir())

	if err := ctx.Append(Message{
		Role:    RoleUser,
		Content: types.ContentParts{types.TextPart{Text: "x"}},
	}); err != nil {
		t.Fatalf("Append(user) unexpected error = %v", err)
	}
	if err := ctx.Append(Message{
		Role:    RoleSystem,
		Content: types.ContentParts{types.TextPart{Text: "summary"}},
	}); err != nil {
		t.Fatalf("Append(system) unexpected error = %v", err)
	}

	if err := ctx.Append(Message{
		Role:       RoleAssistant,
		Content:    types.ContentParts{types.TextPart{Text: "x"}},
		ToolCallID: "call-1",
	}); err == nil || !strings.Contains(err.Error(), "tool_call_id is only allowed") {
		t.Fatalf("Append(assistant with tool_call_id) error = %v", err)
	}

	if err := ctx.Append(Message{
		Role:    RoleTool,
		Content: types.ContentParts{types.TextPart{Text: "x"}},
	}); err == nil || !strings.Contains(err.Error(), "requires tool_call_id") {
		t.Fatalf("Append(tool without tool_call_id) error = %v", err)
	}

	if err := ctx.Append(Message{
		Role:    Role("unknown"),
		Content: types.ContentParts{types.TextPart{Text: "x"}},
	}); err == nil || !strings.Contains(err.Error(), "invalid role") {
		t.Fatalf("Append(unknown role) error = %v", err)
	}
}
