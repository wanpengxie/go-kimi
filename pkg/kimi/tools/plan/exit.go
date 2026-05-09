package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	toolparams "github.com/wanpengxie/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
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
  "additionalProperties": false
}`)

type exitParams struct {
	Decision string `json:"decision"`
	Feedback string `json:"feedback"`
}

// ExitPlanMode implements the exit_plan_mode tool.
type ExitPlanMode struct {
	State        *PlanState
	QuestionFlow QuestionFlow
	Syncer       ModeSyncer
}

// NewExitPlanMode creates one exit_plan_mode tool.
func NewExitPlanMode(state *PlanState) *ExitPlanMode {
	if state == nil {
		state = NewPlanState()
	}
	return &ExitPlanMode{State: state}
}

// ConfigureQuestionFlow enables interactive plan-review decision flow.
func (t *ExitPlanMode) ConfigureQuestionFlow(flow QuestionFlow) *ExitPlanMode {
	if t == nil {
		return t
	}
	t.QuestionFlow = flow
	return t
}

// SetModeSyncer sets one optional plan mode runtime sync hook.
func (t *ExitPlanMode) SetModeSyncer(syncer ModeSyncer) *ExitPlanMode {
	if t == nil {
		return t
	}
	t.Syncer = syncer
	return t
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

	decision := input.Decision
	requestID := ""
	if decision == "" {
		resolvedDecision, resolvedRequestID, dismissed, resolveErr := t.resolveDecision(ctx)
		if resolveErr != nil {
			return buildResult(exitToolName, fmt.Sprintf("exit_plan_mode: %v", resolveErr), true), nil
		}
		if dismissed {
			payload := map[string]any{
				"active":    true,
				"dismissed": true,
			}
			if resolvedRequestID != "" {
				payload["request_id"] = resolvedRequestID
			}
			return buildResult(exitToolName, payload, false), nil
		}
		decision = resolvedDecision
		requestID = resolvedRequestID
	}

	planContent, err := state.Exit()
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("exit_plan_mode: %w", err)
	}
	if t != nil && t.Syncer != nil {
		t.Syncer.OnPlanModeExit()
	}

	payload := map[string]any{
		"decision":     decision,
		"plan_content": limitOutput(planContent),
		"active":       false,
	}
	if requestID != "" {
		payload["request_id"] = requestID
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
	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return exitParams{}, fmt.Errorf("exit_plan_mode: decode params: %w", err)
	}

	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Feedback = strings.TrimSpace(input.Feedback)
	if input.Decision == "" {
		return input, nil
	}
	switch input.Decision {
	case decisionApprove, decisionReject, decisionRevise:
		return input, nil
	default:
		return exitParams{}, errors.New("exit_plan_mode: decision must be approve, reject, or revise")
	}
}

func (t *ExitPlanMode) resolveDecision(ctx context.Context) (string, string, bool, error) {
	if t != nil && t.QuestionFlow.isYolo() {
		return decisionApprove, "", false, nil
	}
	if t == nil || !t.QuestionFlow.enabled() {
		return "", "", false, errors.New("decision is required")
	}

	outcome, err := t.QuestionFlow.askSingleChoice(
		ctx,
		"exit-plan",
		"Review plan and choose one decision.",
		wire.QuestionItem{
			Header:   "Plan Review",
			ID:       "decision",
			Question: "Select a review decision for this plan.",
			Options: []wire.QuestionOption{
				{
					Label:       "Approve",
					Description: "Accept this plan and exit plan mode",
					Value:       decisionApprove,
				},
				{
					Label:       "Revise",
					Description: "Request revisions before implementation",
					Value:       decisionRevise,
				},
				{
					Label:       "Reject",
					Description: "Reject this plan output",
					Value:       decisionReject,
				},
			},
		},
	)
	if err != nil {
		return "", "", false, err
	}

	decision := normalizeDecision(outcome.Answer)
	if decision == "" {
		return "", outcome.RequestID, true, nil
	}
	return decision, outcome.RequestID, false, nil
}

func normalizeDecision(answer string) string {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case decisionApprove, "yes", "true":
		return decisionApprove
	case decisionReject, "no", "false":
		return decisionReject
	case decisionRevise, "edit":
		return decisionRevise
	default:
		return ""
	}
}
