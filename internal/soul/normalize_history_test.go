package soul

import (
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestNormalizeHistoryEmptyReturnsNil(t *testing.T) {
	t.Parallel()

	if got := normalizeHistory(nil); got != nil {
		t.Fatalf("normalizeHistory(nil) = %#v, want nil", got)
	}
	if got := normalizeHistory([]Message{}); got != nil {
		t.Fatalf("normalizeHistory(empty) = %#v, want nil", got)
	}
}

func TestNormalizeHistoryMergesAdjacentAssistantMessages(t *testing.T) {
	t.Parallel()

	history := []Message{
		{
			Role: RoleAssistant,
			Content: types.ContentParts{
				types.TextPart{Text: "first"},
			},
			ToolCalls: []types.ToolCall{
				{ID: "call-1", Name: "search"},
			},
		},
		{
			Role: RoleAssistant,
			Content: types.ContentParts{
				types.TextPart{Text: "second"},
			},
			ToolCalls: []types.ToolCall{
				{ID: "call-2", Name: "read_file"},
			},
		},
	}

	normalized := normalizeHistory(history)
	if len(normalized) != 1 {
		t.Fatalf("normalized message count = %d, want 1", len(normalized))
	}
	if normalized[0].Role != RoleAssistant {
		t.Fatalf("normalized[0].Role = %q, want assistant", normalized[0].Role)
	}
	if len(normalized[0].Content) != 2 {
		t.Fatalf("normalized content part count = %d, want 2", len(normalized[0].Content))
	}
	if got := textFromContentPart(normalized[0].Content[0]); got != "first" {
		t.Fatalf("content[0] = %q, want %q", got, "first")
	}
	if got := textFromContentPart(normalized[0].Content[1]); got != "second" {
		t.Fatalf("content[1] = %q, want %q", got, "second")
	}
	if len(normalized[0].ToolCalls) != 2 {
		t.Fatalf("normalized tool call count = %d, want 2", len(normalized[0].ToolCalls))
	}
	if normalized[0].ToolCalls[0].ID != "call-1" || normalized[0].ToolCalls[1].ID != "call-2" {
		t.Fatalf("normalized tool calls = %#v, want [call-1 call-2]", normalized[0].ToolCalls)
	}
}

func TestNormalizeHistoryDoesNotMergeToolMessagesWithoutToolCallID(t *testing.T) {
	t.Parallel()

	history := []Message{
		{
			Role:    RoleTool,
			Content: types.ContentParts{types.TextPart{Text: "result-1"}},
		},
		{
			Role:    RoleTool,
			Content: types.ContentParts{types.TextPart{Text: "result-2"}},
		},
	}

	normalized := normalizeHistory(history)
	if len(normalized) != 2 {
		t.Fatalf("normalized message count = %d, want 2", len(normalized))
	}
}

func TestNormalizeHistoryDoesNotMergeToolMessagesWithDifferentToolCallID(t *testing.T) {
	t.Parallel()

	history := []Message{
		{
			Role:       RoleTool,
			ToolCallID: "call-1",
			Content:    types.ContentParts{types.TextPart{Text: "result-1"}},
		},
		{
			Role:       RoleTool,
			ToolCallID: "call-2",
			Content:    types.ContentParts{types.TextPart{Text: "result-2"}},
		},
	}

	normalized := normalizeHistory(history)
	if len(normalized) != 2 {
		t.Fatalf("normalized message count = %d, want 2", len(normalized))
	}
}

func TestNormalizeHistoryDoesNotMergeAcrossRoleBoundary(t *testing.T) {
	t.Parallel()

	history := []Message{
		{
			Role:    RoleUser,
			Content: types.ContentParts{types.TextPart{Text: "u1"}},
		},
		{
			Role:    RoleUser,
			Content: types.ContentParts{types.TextPart{Text: "u2"}},
		},
		{
			Role:    RoleAssistant,
			Content: types.ContentParts{types.TextPart{Text: "a1"}},
		},
		{
			Role:    RoleUser,
			Content: types.ContentParts{types.TextPart{Text: "u3"}},
		},
	}

	normalized := normalizeHistory(history)
	if len(normalized) != 3 {
		t.Fatalf("normalized message count = %d, want 3", len(normalized))
	}
	if normalized[0].Role != RoleUser || len(normalized[0].Content) != 2 {
		t.Fatalf("normalized[0] = %#v, want merged first two user messages", normalized[0])
	}
	if normalized[2].Role != RoleUser || len(normalized[2].Content) != 1 {
		t.Fatalf("normalized[2] = %#v, want separate trailing user message", normalized[2])
	}
	if got := textFromContentPart(normalized[2].Content[0]); got != "u3" {
		t.Fatalf("normalized trailing user text = %q, want %q", got, "u3")
	}
}
