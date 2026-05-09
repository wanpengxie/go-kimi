package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	taskStopToolName        = "task_stop"
	taskStopToolDescription = "Request cancellation for one background task."
)

var taskStopSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "Task id to stop"
    },
    "reason": {
      "type": "string",
      "description": "Optional kill reason"
    }
  },
  "required": ["task_id"],
  "additionalProperties": false
}`)

// TaskStopTool requests one task stop signal via manager.
type TaskStopTool struct {
	Manager TaskManager
}

type taskStopParams struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// NewTaskStop creates one task_stop tool.
func NewTaskStop(manager TaskManager) *TaskStopTool {
	return &TaskStopTool{Manager: manager}
}

// Name returns the tool name.
func (*TaskStopTool) Name() string {
	return taskStopToolName
}

// Description returns the tool description.
func (*TaskStopTool) Description() string {
	return taskStopToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*TaskStopTool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(taskStopSchema)
}

// Execute sends kill request and returns the latest task summary.
func (t *TaskStopTool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeTaskStopParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}
	if t == nil || t.Manager == nil {
		return buildErrorResult(taskStopToolName, "task_stop: manager is not configured"), nil
	}

	if err := t.Manager.KillTask(input.TaskID, input.Reason); err != nil {
		return buildErrorResult(taskStopToolName, fmt.Sprintf("task_stop: kill task: %v", err)), nil
	}

	view, err := t.Manager.GetTask(input.TaskID)
	if err != nil {
		return buildResult(taskStopToolName, map[string]any{
			"task_id": input.TaskID,
			"status":  "kill_requested",
			"reason":  fallbackKillReason(input.Reason),
		}, false), nil
	}

	return buildResult(taskStopToolName, map[string]any{
		"task":    summarizeTask(view),
		"message": "kill requested",
	}, false), nil
}

func decodeTaskStopParams(raw json.RawMessage) (taskStopParams, error) {
	input := taskStopParams{}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return taskStopParams{}, fmt.Errorf("task_stop: decode params: %w", err)
		}
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return taskStopParams{}, errors.New("task_stop: task_id is required")
	}
	input.Reason = strings.TrimSpace(input.Reason)
	return input, nil
}

func fallbackKillReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "killed by request"
	}
	return reason
}
