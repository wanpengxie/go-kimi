package soul

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

// Run executes one turn loop with optional tool call iterations.
func (s *Soul) Run(ctx context.Context, input types.ContentParts) (StepResult, error) {
	return s.run(ctx, input)
}

func (s *Soul) run(ctx context.Context, input types.ContentParts) (StepResult, error) {
	if err := s.ensureReady(); err != nil {
		return StepResult{}, err
	}

	turnID := newTurnID()
	if err := s.emit(wire.TurnBegin{
		TurnID: turnID,
		Input:  cloneContentParts(input),
	}); err != nil {
		return StepResult{}, err
	}

	if err := s.context.Append(Message{
		Role:    RoleUser,
		Content: cloneContentParts(input),
	}); err != nil {
		return StepResult{}, fmt.Errorf("soul run: append user message: %w", err)
	}

	stopReason := "max_steps"
	finalResult := StepResult{}
	for stepIndex := 0; stepIndex < s.maxSteps; stepIndex++ {
		stepResult, err := s.step(ctx, turnID)
		if err != nil {
			return StepResult{}, err
		}
		finalResult = stepResult

		if err := s.context.Append(Message{
			Role:      RoleAssistant,
			Content:   cloneContentParts(stepResult.Content),
			ToolCalls: cloneToolCalls(stepResult.ToolCalls),
		}); err != nil {
			return StepResult{}, fmt.Errorf("soul run: append assistant message: %w", err)
		}

		for i := range stepResult.ToolResults {
			toolResult := stepResult.ToolResults[i]
			toolCallID := strings.TrimSpace(toolResult.ToolCallID)
			if toolCallID == "" {
				toolCallID = fmt.Sprintf("tool_call_%d", i)
			}
			if err := s.context.Append(Message{
				Role:       RoleTool,
				Content:    toolResultContent(toolResult),
				ToolCallID: toolCallID,
			}); err != nil {
				return StepResult{}, fmt.Errorf("soul run: append tool message: %w", err)
			}
		}

		if err := s.postStepCompaction(ctx); err != nil {
			return StepResult{}, fmt.Errorf("soul run: post-step compaction: %w", err)
		}

		if len(stepResult.ToolCalls) == 0 {
			stopReason = "stop"
			break
		}
	}

	if err := s.emit(wire.TurnEnd{
		TurnID:     turnID,
		StopReason: stopReason,
		Output:     cloneContentParts(finalResult.Content),
		Usage:      usagePtr(finalResult.Usage),
	}); err != nil {
		return StepResult{}, err
	}

	return finalResult, nil
}

func toolResultContent(result types.ToolResult) types.ContentParts {
	return types.ContentParts{
		types.TextPart{
			Text: stringifyToolValue(result.Value.Value),
		},
	}
}

func stringifyToolValue(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}

func usagePtr(usage types.TokenUsage) *types.TokenUsage {
	if usage == (types.TokenUsage{}) {
		return nil
	}
	copied := usage
	return &copied
}

func newTurnID() string {
	return fmt.Sprintf("turn-%d", time.Now().UTC().UnixNano())
}
