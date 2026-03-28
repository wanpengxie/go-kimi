package subagents

import (
	"fmt"
	"strings"
	"sync"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	transcriptStageText       = "text"
	transcriptStageToolCall   = "tool_call"
	transcriptStageToolResult = "result"
	transcriptStageSummary    = "summary"
	transcriptStageError      = "error"
)

type transcriptRecord struct {
	Stage      string            `json:"stage"`
	Text       string            `json:"text,omitempty"`
	ToolCall   *types.ToolCall   `json:"tool_call,omitempty"`
	ToolResult *types.ToolResult `json:"tool_result,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// SubagentOutputWriter collects one structured subagent execution transcript.
type SubagentOutputWriter struct {
	mu      sync.Mutex
	records []transcriptRecord
}

func newSubagentOutputWriter() *SubagentOutputWriter {
	return &SubagentOutputWriter{
		records: make([]transcriptRecord, 0, 16),
	}
}

func (w *SubagentOutputWriter) ObserveWireMessage(msg wire.WireMessage) {
	if w == nil || msg == nil {
		return
	}

	switch typed := msg.(type) {
	case wire.TextDelta:
		text := strings.TrimSpace(typed.Delta)
		if text == "" {
			return
		}
		w.append(transcriptRecord{
			Stage: transcriptStageText,
			Text:  text,
		})
	case wire.ToolCallRequest:
		call := typed.ToolCall
		w.append(transcriptRecord{
			Stage:    transcriptStageToolCall,
			ToolCall: &call,
		})
	case wire.ToolCallResult:
		result := typed.Result
		w.append(transcriptRecord{
			Stage:      transcriptStageToolResult,
			ToolResult: &result,
		})
	case wire.TurnEnd:
		summary := strings.TrimSpace(contentPartsText(typed.Output))
		if summary == "" {
			return
		}
		w.append(transcriptRecord{
			Stage:   transcriptStageSummary,
			Summary: summary,
		})
	case wire.CompactionError:
		w.RecordError(typed.Error)
	}
}

func (w *SubagentOutputWriter) RecordError(value any) {
	if w == nil {
		return
	}
	errText := strings.TrimSpace(fmt.Sprint(value))
	if errText == "" || errText == "<nil>" {
		return
	}
	w.append(transcriptRecord{
		Stage: transcriptStageError,
		Error: errText,
	})
}

func (w *SubagentOutputWriter) Snapshot() []map[string]any {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.records) == 0 {
		return nil
	}

	out := make([]map[string]any, 0, len(w.records))
	for i := range w.records {
		out = append(out, snapshotRecord(w.records[i]))
	}
	return out
}

func (w *SubagentOutputWriter) append(record transcriptRecord) {
	if w == nil {
		return
	}
	if strings.TrimSpace(record.Stage) == "" {
		return
	}
	w.mu.Lock()
	w.records = append(w.records, record)
	w.mu.Unlock()
}

func snapshotRecord(record transcriptRecord) map[string]any {
	payload := map[string]any{
		"stage": record.Stage,
	}
	if text := strings.TrimSpace(record.Text); text != "" {
		payload["text"] = text
	}
	if record.ToolCall != nil {
		call := *record.ToolCall
		payload["tool_call"] = call
	}
	if record.ToolResult != nil {
		result := *record.ToolResult
		payload["tool_result"] = result
	}
	if summary := strings.TrimSpace(record.Summary); summary != "" {
		payload["summary"] = summary
	}
	if errText := strings.TrimSpace(record.Error); errText != "" {
		payload["error"] = errText
	}
	return payload
}
