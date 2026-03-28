package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	exitToolName        = "exit_plan_mode"
	exitToolDescription = "Exit plan mode with decision and return plan content."
)

const (
	decisionApprove = "approve"
	decisionReject  = "reject"
	decisionRevise  = "revise"
)

var exitParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "decision": {
      "type": "string",
      "enum": ["approve", "reject", "revise"],
      "description": "Plan review decision"
    },
    "feedback": {
      "type": "string",
      "description": "Optional decision feedback"
    }
  },
  "required": ["decision"],
  "additionalProperties": false
}`)

type exitParams struct {
	Decision string `json:"decision"`
	Feedback string `json:"feedback"`
}

// ExitPlanMode implements the exit_plan_mode tool.
type ExitPlanMode struct {
	State *PlanState
}

// NewExitPlanMode creates one exit_plan_mode tool.
func NewExitPlanMode(state *PlanState) *ExitPlanMode {
	if state == nil {
		state = NewPlanState()
	}
	return &ExitPlanMode{State: state}
}

// Name returns the tool name.
func (*ExitPlanMode) Name() string {
	return exitToolName
}

// Description returns the tool description.
func (*ExitPlanMode) Description() string {
	return exitToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*ExitPlanMode) ParameterSchema() json.RawMessage {
	return cloneRawMessage(exitParameterSchema)
}

// Execute exits plan mode, reads plan content and returns it with one decision.
func (t *ExitPlanMode) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeExitParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	state := t.planState()
	if state == nil {
		return types.ToolResult{}, errors.New("exit_plan_mode: state is unavailable")
	}

	planContent, err := state.Exit()
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("exit_plan_mode: %w", err)
	}

	payload := map[string]any{
		"decision":     input.Decision,
		"plan_content": limitOutput(planContent),
		"active":       false,
	}
	if input.Feedback != "" {
		payload["feedback"] = input.Feedback
	}
	return buildResult(exitToolName, payload, false), nil
}

func (t *ExitPlanMode) planState() *PlanState {
	if t == nil {
		return nil
	}
	if t.State == nil {
		t.State = NewPlanState()
	}
	return t.State
}

func decodeExitParams(raw json.RawMessage) (exitParams, error) {
	input := exitParams{}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return exitParams{}, fmt.Errorf("exit_plan_mode: decode params: %w", err)
		}
	}

	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Feedback = strings.TrimSpace(input.Feedback)
	switch input.Decision {
	case decisionApprove, decisionReject, decisionRevise:
		return input, nil
	case "":
		return exitParams{}, errors.New("exit_plan_mode: decision is required")
	default:
		return exitParams{}, errors.New("exit_plan_mode: decision must be approve, reject, or revise")
	}
}
