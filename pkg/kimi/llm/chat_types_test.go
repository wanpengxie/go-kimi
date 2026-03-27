package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestChatRequestJSONShape(t *testing.T) {
	t.Parallel()

	req := ChatRequest{
		Messages: []Message{{
			Role: "user",
			Content: types.ContentParts{
				types.TextPart{Text: "hello"},
			},
		}},
		Tools: []ToolDefinition{{
			Name:       "search",
			Parameters: map[string]any{"type": "object"},
		}},
		Temperature: 0.2,
		MaxTokens:   512,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(ChatRequest) error = %v", err)
	}
	text := string(payload)
	for _, key := range []string{"\"messages\"", "\"tools\"", "\"max_tokens\""} {
		if !strings.Contains(text, key) {
			t.Fatalf("chat request json missing key %s: %s", key, text)
		}
	}
}

func TestChatEventErrorNotSerialized(t *testing.T) {
	t.Parallel()

	event := ChatEvent{
		Err:  assertErr{},
		Done: true,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(ChatEvent) error = %v", err)
	}
	if strings.Contains(string(payload), "Err") || strings.Contains(string(payload), "error") {
		t.Fatalf("chat event must not serialize error field: %s", payload)
	}
}

type assertErr struct{}

func (assertErr) Error() string {
	return "assert"
}
