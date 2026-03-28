package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	toolparams "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	toolName        = "agent"
	toolDescription = "Delegate one task to a subagent in foreground or background."

	defaultSubagentType = "general-purpose"
)

var parameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "agent_id": {
      "type": "string",
      "description": "Existing agent id to resume"
    },
    "prompt": {
      "type": "string",
      "description": "Task description passed to the subagent"
    },
    "subagent_type": {
      "type": "string",
      "default": "general-purpose",
      "description": "Subagent type name"
    },
    "run_in_background": {
      "type": "boolean",
      "default": false,
      "description": "Run the delegated task as a background task"
    },
    "model_override": {
      "type": "string",
      "description": "Override model name for this run"
    }
  },
  "required": ["prompt"],
  "additionalProperties": false
}`)

// ForegroundRunner defines the foreground subagent execution dependency.
type ForegroundRunner interface {
	Run(ctx context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error)
}

// BackgroundManager defines the background task creation dependency.
type BackgroundManager interface {
	CreateAgentTask(ctx context.Context, spec corebg.TaskSpec) (string, error)
}

// Tool implements the model-callable agent delegation tool.
type Tool struct {
	ForegroundRunner  ForegroundRunner
	BackgroundManager BackgroundManager
	SessionID         string
	TimeoutSec        int
}

type executeParams struct {
	AgentID         string `json:"agent_id"`
	Prompt          string `json:"prompt"`
	SubagentType    string `json:"subagent_type"`
	RunInBackground bool   `json:"run_in_background"`
	ModelOverride   string `json:"model_override"`
}

// New creates one agent tool.
func New(foregroundRunner ForegroundRunner, backgroundManager BackgroundManager) *Tool {
	return &Tool{
		ForegroundRunner:  foregroundRunner,
		BackgroundManager: backgroundManager,
	}
}

// Name returns the tool name.
func (*Tool) Name() string {
	return toolName
}

// Description returns the tool description.
func (*Tool) Description() string {
	return toolDescription
}

// ParameterSchema returns the JSON schema for tool parameters.
func (*Tool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(parameterSchema)
}

// Execute delegates one task to a foreground or background subagent path.
func (t *Tool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	if input.RunInBackground {
		return t.executeBackground(ctx, input), nil
	}
	return t.executeForeground(ctx, input), nil
}

func (t *Tool) executeForeground(ctx context.Context, input executeParams) types.ToolResult {
	if t == nil || t.ForegroundRunner == nil {
		return toolErrorResult("agent tool: foreground runner is not configured")
	}

	output, err := t.ForegroundRunner.Run(ctx, subagents.ForegroundRunRequest{
		AgentID:       input.AgentID,
		SubagentType:  input.SubagentType,
		Prompt:        input.Prompt,
		ModelOverride: input.ModelOverride,
	})
	if err != nil {
		return toolErrorResult(fmt.Sprintf("agent tool: run foreground subagent: %v", err))
	}

	value := output.Value
	if payload, ok := output.Value.(map[string]any); ok {
		value = mergeMap(payload, map[string]any{
			"run_in_background": false,
		})
	}

	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: value,
		},
	}
}

func (t *Tool) executeBackground(ctx context.Context, input executeParams) types.ToolResult {
	if t == nil || t.BackgroundManager == nil {
		return toolErrorResult("agent tool: background manager is not configured")
	}

	spec := corebg.TaskSpec{
		SessionID:     strings.TrimSpace(t.SessionID),
		Description:   input.Prompt,
		AgentID:       input.AgentID,
		SubagentType:  input.SubagentType,
		Prompt:        input.Prompt,
		ModelOverride: input.ModelOverride,
		TimeoutSec:    nonNegative(t.TimeoutSec),
	}
	taskID, err := t.BackgroundManager.CreateAgentTask(ctx, spec)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("agent tool: create background task: %v", err))
	}

	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: mergeMap(map[string]any{
				"task_id":           taskID,
				"status":            string(corebg.TaskCreated),
				"subagent_type":     input.SubagentType,
				"run_in_background": true,
			}, optionalBackgroundFields(input)),
		},
	}
}

func decodeParams(raw json.RawMessage) (executeParams, error) {
	input := executeParams{}
	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return executeParams{}, fmt.Errorf("agent tool: decode params: %w", err)
	}

	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return executeParams{}, errors.New("agent tool: prompt is required")
	}
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.SubagentType = strings.TrimSpace(input.SubagentType)
	if input.SubagentType == "" {
		input.SubagentType = defaultSubagentType
	}
	input.ModelOverride = strings.TrimSpace(input.ModelOverride)
	return input, nil
}

func optionalBackgroundFields(input executeParams) map[string]any {
	extras := map[string]any{}
	if input.AgentID != "" {
		extras["agent_id"] = input.AgentID
	}
	if input.ModelOverride != "" {
		extras["model_override"] = input.ModelOverride
	}
	return extras
}

func toolErrorResult(message string) types.ToolResult {
	return types.ToolResult{
		Name:    toolName,
		IsError: true,
		Value: types.ToolReturnValue{
			Value: strings.TrimSpace(message),
		},
	}
}

func mergeMap(base map[string]any, extras map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extras))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extras {
		out[key] = value
	}
	return out
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
