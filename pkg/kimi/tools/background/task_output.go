package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corebg "github.com/wanpengxie/go-kimi/pkg/kimi/background"
	toolparams "github.com/wanpengxie/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
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
	    "consumer_id": {
	      "type": "string",
	      "description": "Optional consumer cursor id; when set, output is read from consumer state"
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
	TaskID     string `json:"task_id"`
	ConsumerID string `json:"consumer_id"`
	Offset     int64  `json:"offset"`
	MaxBytes   int    `json:"max_bytes"`
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
	var chunk corebg.TaskOutputChunk
	if input.ConsumerID != "" {
		chunk, err = t.Manager.ReadConsumerOutput(input.TaskID, input.ConsumerID, maxBytes)
	} else {
		chunk, err = t.Manager.TailOutput(input.TaskID, input.Offset, maxBytes)
	}
	if err != nil {
		return buildErrorResult(taskOutputToolName, fmt.Sprintf("task_output: read output: %v", err)), nil
	}
	output := limitOutput(chunk.Output)
	chunkPayload := map[string]any{
		"task_id":     chunk.TaskID,
		"status":      string(chunk.Status),
		"offset":      chunk.Offset,
		"next_offset": chunk.NextOffset,
		"output":      output,
		"eof":         chunk.EOF,
	}
	if chunk.ConsumerID != "" {
		chunkPayload["consumer_id"] = chunk.ConsumerID
	}

	return buildResult(taskOutputToolName, map[string]any{
		"task":        summarizeTask(view),
		"chunk":       chunkPayload,
		"output":      output,
		"offset":      chunk.Offset,
		"next_offset": chunk.NextOffset,
		"status":      string(chunk.Status),
		"eof":         chunk.EOF,
		"max_bytes":   maxBytes,
	}, false), nil
}

func decodeTaskOutputParams(raw json.RawMessage) (taskOutputParams, error) {
	input := taskOutputParams{}

	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return taskOutputParams{}, fmt.Errorf("task_output: decode params: %w", err)
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return taskOutputParams{}, errors.New("task_output: task_id is required")
	}
	input.ConsumerID = strings.TrimSpace(input.ConsumerID)
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
