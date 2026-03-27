package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestWireFileAppendAndIterRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := NewWireFile(path)

	if !wireFile.IsEmpty() {
		t.Fatal("new wire file should be empty")
	}

	messages := []WireMessage{
		TurnBegin{
			TurnID: "turn-1",
			Input: types.ContentParts{
				types.TextPart{Text: "hello"},
			},
		},
		ToolCallRequest{
			ID: "req-1",
			ToolCall: types.ToolCall{
				ID:   "call-1",
				Name: "search",
				Arguments: map[string]any{
					"q": "go test",
				},
			},
		},
		QuestionResponse{
			RequestID: "question-1",
			Answers: map[string]string{
				"choice": "A",
			},
		},
	}

	for i := range messages {
		if err := wireFile.AppendMessage(messages[i]); err != nil {
			t.Fatalf("AppendMessage[%d]() error = %v", i, err)
		}
	}

	if wireFile.IsEmpty() {
		t.Fatal("wire file should not be empty after append")
	}

	it, err := wireFile.IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() error = %v", err)
	}
	defer func() {
		if closeErr := it.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()

	var records []WireMessageRecord
	for it.Next() {
		records = append(records, it.Record())
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator err = %v", err)
	}

	if len(records) != len(messages) {
		t.Fatalf("record count = %d, want %d", len(records), len(messages))
	}

	for i := range records {
		if records[i].Timestamp.IsZero() {
			t.Fatalf("record[%d] timestamp should not be zero", i)
		}
		decoded, err := records[i].Message()
		if err != nil {
			t.Fatalf("record[%d].Message() error = %v", i, err)
		}
		assertSameMessage(t, decoded, messages[i])
	}
}

func TestWireFileIterRecordsOnMissingFile(t *testing.T) {
	t.Parallel()

	wireFile := NewWireFile(filepath.Join(t.TempDir(), "missing.jsonl"))
	if !wireFile.IsEmpty() {
		t.Fatal("missing file should be treated as empty")
	}

	it, err := wireFile.IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() on missing file error = %v", err)
	}
	defer func() {
		if closeErr := it.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()

	if it.Next() {
		t.Fatal("Next() should be false for missing file")
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator err = %v", err)
	}
}

func TestWireFileIterRecordsInvalidJSONLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	wireFile := NewWireFile(path)
	it, err := wireFile.IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() error = %v", err)
	}
	defer func() {
		if closeErr := it.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()

	if it.Next() {
		t.Fatal("Next() should be false when first line is invalid JSON")
	}
	if err := it.Err(); err == nil {
		t.Fatal("iterator err expected on invalid JSON line")
	} else if !strings.Contains(err.Error(), "parse line 1") {
		t.Fatalf("iterator err = %v, want parse line context", err)
	}
}

func TestWireFilePathValidation(t *testing.T) {
	t.Parallel()

	var nilFile *WireFile
	if err := nilFile.AppendMessage(TurnBegin{TurnID: "turn-1"}); err == nil {
		t.Fatal("nil WireFile AppendMessage expected error")
	}

	empty := &WireFile{}
	if err := empty.AppendMessage(TurnBegin{TurnID: "turn-1"}); err == nil {
		t.Fatal("AppendMessage() with empty path expected error")
	}
	if _, err := empty.IterRecords(); err == nil {
		t.Fatal("IterRecords() with empty path expected error")
	}
	if !empty.IsEmpty() {
		t.Fatal("empty-path WireFile should be considered empty")
	}
}
