package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	taskOutputToolName        = "task_output"
	taskOutputToolDescription = "Read one background task status and output."

	defaultTaskOutputMaxBytes = 16 * 1024
	maxTaskOutputBytes        = corebg.MaxTaskOutputBytes
)

var taskOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "Task id to inspect"
    },
    "offset": {
      "type": "integer",
      "minimum": 0,
      "default": 0,
      "description": "Byte offset in output log"
    },
    "max_bytes": {
      "type": "integer",
      "minimum": 0,
      "maximum": 1048576,
      "default": 16384,
      "description": "Maximum bytes to read from output log"
    }
  },
  "required": ["task_id"],
  "additionalProperties": false
}`)

// TaskOutputTool returns task runtime snapshot and output fragment.
type TaskOutputTool struct {
	Manager       TaskManager
	DefaultMaxLen int
}

type taskOutputParams struct {
	TaskID   string `json:"task_id"`
	Offset   int64  `json:"offset"`
	MaxBytes int    `json:"max_bytes"`
}

// NewTaskOutput creates one task_output tool.
func NewTaskOutput(manager TaskManager) *TaskOutputTool {
	return &TaskOutputTool{
		Manager:       manager,
		DefaultMaxLen: defaultTaskOutputMaxBytes,
	}
}

// Name returns the tool name.
func (*TaskOutputTool) Name() string {
	return taskOutputToolName
}

// Description returns the tool description.
func (*TaskOutputTool) Description() string {
	return taskOutputToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*TaskOutputTool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(taskOutputSchema)
}

// Execute reads task view and output bytes from the manager.
func (t *TaskOutputTool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeTaskOutputParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}
	if t == nil || t.Manager == nil {
		return buildErrorResult(taskOutputToolName, "task_output: manager is not configured"), nil
	}

	view, err := t.Manager.GetTask(input.TaskID)
	if err != nil {
		return buildErrorResult(taskOutputToolName, fmt.Sprintf("task_output: get task: %v", err)), nil
	}

	maxBytes := input.MaxBytes
	if maxBytes == 0 {
		maxBytes = t.defaultMaxLen()
	}
	output, err := t.Manager.ReadOutput(input.TaskID, input.Offset, maxBytes)
	if err != nil {
		return buildErrorResult(taskOutputToolName, fmt.Sprintf("task_output: read output: %v", err)), nil
	}

	return buildResult(taskOutputToolName, map[string]any{
		"task":      summarizeTask(view),
		"output":    limitOutput(string(output)),
		"offset":    input.Offset,
		"max_bytes": maxBytes,
	}, false), nil
}

func decodeTaskOutputParams(raw json.RawMessage) (taskOutputParams, error) {
	input := taskOutputParams{}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return taskOutputParams{}, fmt.Errorf("task_output: decode params: %w", err)
		}
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return taskOutputParams{}, errors.New("task_output: task_id is required")
	}
	if input.Offset < 0 {
		return taskOutputParams{}, errors.New("task_output: offset must be >= 0")
	}
	if input.MaxBytes < 0 {
		return taskOutputParams{}, errors.New("task_output: max_bytes must be >= 0")
	}
	if input.MaxBytes > maxTaskOutputBytes {
		return taskOutputParams{}, fmt.Errorf("task_output: max_bytes must be <= %d", maxTaskOutputBytes)
	}
	return input, nil
}

func (t *TaskOutputTool) defaultMaxLen() int {
	if t == nil || t.DefaultMaxLen <= 0 {
		return defaultTaskOutputMaxBytes
	}
	return t.DefaultMaxLen
}
