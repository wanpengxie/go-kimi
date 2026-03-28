package soul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	s.beginTurnRuntime(turnID)
	defer s.endTurnRuntime(turnID)

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
		s.setCurrentStep(stepIndex + 1)

		stepResult, err := s.stepWithRetry(ctx, turnID, stepIndex+1)
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
			s.handleCompactionFailure(err)
		}

		steerApplied, err := s.consumeSteerInputs(turnID)
		if err != nil {
			return StepResult{}, err
		}
		if steerApplied {
			continue
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

func (s *Soul) stepWithRetry(ctx context.Context, turnID string, stepIndex int) (StepResult, error) {
	if s == nil {
		return StepResult{}, errors.New("soul run: nil")
	}

	cfg := s.stepRetryConfigSnapshot()
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result, err := s.step(ctx, turnID)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if attempt >= cfg.MaxRetries || !shouldRetryStepError(err) {
			break
		}

		reason := fmt.Sprintf("step %d retry %d/%d: %v", stepIndex, attempt+1, cfg.MaxRetries, err)
		if emitErr := s.emit(wire.StepInterrupted{
			StepID: fmt.Sprintf("%s-step-%d", turnID, stepIndex),
			Reason: reason,
		}); emitErr != nil {
			return StepResult{}, emitErr
		}
	}
	return StepResult{}, fmt.Errorf("soul run: step %d failed after %d retries: %w", stepIndex, cfg.MaxRetries, lastErr)
}

func shouldRetryStepError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func (s *Soul) consumeSteerInputs(turnID string) (bool, error) {
	if s == nil || s.steerCh == nil {
		return false, nil
	}

	turnID = strings.TrimSpace(turnID)
	consumed := false
	for {
		select {
		case request := <-s.steerCh:
			if strings.TrimSpace(request.TurnID) != turnID {
				continue
			}

			text := strings.TrimSpace(request.Text)
			if text == "" {
				continue
			}

			consumed = true
			if err := s.context.Append(Message{
				Role: RoleUser,
				Content: types.ContentParts{
					types.TextPart{Text: text},
				},
			}); err != nil {
				return consumed, fmt.Errorf("soul run: append steer message: %w", err)
			}
			if err := s.emit(wire.SteerInput{
				Text:     text,
				Priority: strings.TrimSpace(request.Priority),
			}); err != nil {
				return consumed, err
			}
		default:
			return consumed, nil
		}
	}
}

func (s *Soul) handleCompactionFailure(err error) {
	if err == nil {
		return
	}

	log.Printf("WARN soul run: post-step compaction failed: %v", err)
	if emitErr := s.emit(wire.CompactionError{
		Error: err.Error(),
	}); emitErr != nil {
		log.Printf("WARN soul run: emit compaction error event failed: %v", emitErr)
	}
}
