package background

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	lineTruncateSuffix   = "...[line-truncated]"
	outputTruncateSuffix = "\n...[truncated]"
)

// TaskManager defines operations needed by background tools.
type TaskManager interface {
	GetTask(taskID string) (*corebg.TaskView, error)
	ListTasks(limit int) ([]*corebg.TaskView, error)
	ReadOutput(taskID string, offset int64, maxBytes int) ([]byte, error)
	TailOutput(taskID string, offset int64, maxBytes int) (corebg.TaskOutputChunk, error)
	ReadConsumerOutput(taskID string, consumerID string, maxBytes int) (corebg.TaskOutputChunk, error)
	KillTask(taskID string, reason string) error
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func buildResult(name string, value any, isError bool) types.ToolResult {
	return types.ToolResult{
		Name: name,
		Value: types.ToolReturnValue{
			Value: value,
		},
		IsError: isError,
	}
}

func buildErrorResult(name, message string) types.ToolResult {
	return buildResult(name, strings.TrimSpace(message), true)
}

func limitOutput(text string) string {
	limitedLines := truncateEachLine(text, tools.MaxLineLengthChars)
	return truncateWithSuffix(limitedLines, tools.MaxOutputChars, outputTruncateSuffix)
}

func truncateEachLine(text string, limit int) string {
	if text == "" || limit <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = truncateWithSuffix(lines[i], limit, lineTruncateSuffix)
	}
	return strings.Join(lines, "\n")
}

func truncateWithSuffix(text string, limit int, suffix string) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}

	suffixLen := utf8.RuneCountInString(suffix)
	if suffixLen >= limit {
		return firstNRunes(text, limit)
	}

	return firstNRunes(text, limit-suffixLen) + suffix
}

func firstNRunes(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= n {
		return text
	}

	count := 0
	for idx := range text {
		if count == n {
			return text[:idx]
		}
		count++
	}
	return text
}

func summarizeTask(view *corebg.TaskView) map[string]any {
	if view == nil {
		return nil
	}

	summary := map[string]any{
		"task_id":     view.Spec.ID,
		"kind":        string(view.Spec.Kind),
		"status":      string(view.Runtime.Status),
		"description": view.Spec.Description,
		"session_id":  view.Spec.SessionID,
	}
	if view.Spec.SubagentType != "" {
		summary["subagent_type"] = view.Spec.SubagentType
	}
	if view.Spec.Command != "" {
		summary["command"] = view.Spec.Command
	}
	if view.Runtime.StartedAt != nil {
		summary["started_at"] = *view.Runtime.StartedAt
	}
	if view.Runtime.HeartbeatAt != nil {
		summary["heartbeat_at"] = *view.Runtime.HeartbeatAt
	}
	if view.Runtime.FinishedAt != nil {
		summary["finished_at"] = *view.Runtime.FinishedAt
	}
	if view.Runtime.ExitCode != nil {
		summary["exit_code"] = *view.Runtime.ExitCode
	}
	summary["timed_out"] = view.Runtime.TimedOut
	if view.Runtime.FailureReason != "" {
		summary["failure_reason"] = view.Runtime.FailureReason
	}
	if view.Control.KillRequestedAt != nil {
		summary["kill_requested_at"] = *view.Control.KillRequestedAt
	}
	if view.Control.KillReason != "" {
		summary["kill_reason"] = view.Control.KillReason
	}
	return summary
}
