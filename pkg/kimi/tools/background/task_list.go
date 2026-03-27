package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	taskListToolName        = "task_list"
	taskListToolDescription = "List background tasks with summary status."

	defaultTaskListLimit = 20
	maxTaskListLimit     = 200
)

var taskListSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 200,
      "default": 20,
      "description": "Maximum number of tasks to return"
    }
  },
  "additionalProperties": false
}`)

// TaskListTool returns one summary list of background tasks.
type TaskListTool struct {
	Manager TaskManager
}

type taskListParams struct {
	Limit int `json:"limit"`
}

// NewTaskList creates one task_list tool.
func NewTaskList(manager TaskManager) *TaskListTool {
	return &TaskListTool{Manager: manager}
}

// Name returns the tool name.
func (*TaskListTool) Name() string {
	return taskListToolName
}

// Description returns the tool description.
func (*TaskListTool) Description() string {
	return taskListToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*TaskListTool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(taskListSchema)
}

// Execute lists tasks via manager and returns summaries.
func (t *TaskListTool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeTaskListParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}
	if t == nil || t.Manager == nil {
		return buildErrorResult(taskListToolName, "task_list: manager is not configured"), nil
	}

	views, err := t.Manager.ListTasks(input.Limit)
	if err != nil {
		return buildErrorResult(taskListToolName, fmt.Sprintf("task_list: list tasks: %v", err)), nil
	}

	tasks := make([]map[string]any, 0, len(views))
	for i := range views {
		tasks = append(tasks, summarizeTask(views[i]))
	}

	return buildResult(taskListToolName, map[string]any{
		"tasks": tasks,
		"count": len(tasks),
		"limit": input.Limit,
	}, false), nil
}

func decodeTaskListParams(raw json.RawMessage) (taskListParams, error) {
	input := taskListParams{
		Limit: defaultTaskListLimit,
	}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return taskListParams{}, fmt.Errorf("task_list: decode params: %w", err)
		}
	}

	if input.Limit == 0 {
		input.Limit = defaultTaskListLimit
	}
	if input.Limit < 1 {
		return taskListParams{}, errors.New("task_list: limit must be >= 1")
	}
	if input.Limit > maxTaskListLimit {
		return taskListParams{}, fmt.Errorf("task_list: limit must be <= %d", maxTaskListLimit)
	}
	return input, nil
}
