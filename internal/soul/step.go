package soul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func (s *Soul) step(ctx context.Context, turnID string) (StepResult, error) {
	if err := s.ensureReady(); err != nil {
		return StepResult{}, err
	}

	stream, err := s.provider.ChatStream(ctx, llm.ChatRequest{
		Messages: s.buildChatMessages(),
		Tools:    s.toolDefinitions(),
	})
	if err != nil {
		return StepResult{}, fmt.Errorf("soul step: chat stream: %w", err)
	}
	if stream == nil {
		return StepResult{}, errors.New("soul step: nil chat stream")
	}

	result := StepResult{
		Content:   make(types.ContentParts, 0, 8),
		ToolCalls: make([]types.ToolCall, 0, 4),
	}

	for event := range stream {
		if event.Err != nil {
			return StepResult{}, fmt.Errorf("soul step: stream event: %w", event.Err)
		}
		if event.Delta != nil {
			result.Content = append(result.Content, event.Delta)
			if textDelta := textFromContentPart(event.Delta); textDelta != "" {
				if err := s.emit(wire.TextDelta{
					TurnID: turnID,
					Delta:  textDelta,
				}); err != nil {
					return StepResult{}, err
				}
			}
		}
		if event.ToolCall != nil {
			call := normalizeToolCall(*event.ToolCall, len(result.ToolCalls))
			result.ToolCalls = append(result.ToolCalls, call)
		}
		if event.Usage != nil {
			result.Usage = *event.Usage
		}
		if event.Done {
			break
		}
	}

	toolResults, err := s.executeTools(ctx, result.ToolCalls)
	if err != nil {
		return StepResult{}, err
	}
	result.ToolResults = toolResults
	return result, nil
}

func (s *Soul) executeTools(ctx context.Context, toolCalls []types.ToolCall) ([]types.ToolResult, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	results := make([]types.ToolResult, len(toolCalls))
	var wg sync.WaitGroup
	for i := range toolCalls {
		call := toolCalls[i]
		if err := s.emit(wire.ToolCallRequest{
			ID:       call.ID,
			ToolCall: call,
		}); err != nil {
			return nil, err
		}

		wg.Add(1)
		go func(idx int, toolCall types.ToolCall) {
			defer wg.Done()

			approved, feedback := s.requestToolApproval(ctx, toolCall)
			if !approved {
				results[idx] = toolRejectedResult(toolCall, feedback)
				return
			}

			results[idx] = s.executeOneTool(ctx, toolCall)
		}(i, call)
	}
	wg.Wait()

	for i := range results {
		if err := s.emit(wire.ToolCallResult{
			ID:     toolCalls[i].ID,
			Result: results[i],
		}); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func (s *Soul) executeOneTool(ctx context.Context, call types.ToolCall) types.ToolResult {
	executor, ok := s.lookupExecutor(call.Name)
	if !ok {
		return toolErrorResult(call, fmt.Sprintf("tool executor not found: %s", call.Name))
	}

	result, err := executor.Execute(ctx, call)
	if err != nil {
		return toolErrorResult(call, err.Error())
	}

	result.ToolCallID = strings.TrimSpace(result.ToolCallID)
	if result.ToolCallID == "" {
		result.ToolCallID = call.ID
	}
	result.Name = strings.TrimSpace(result.Name)
	if result.Name == "" {
		result.Name = call.Name
	}
	return result
}

func (s *Soul) requestToolApproval(ctx context.Context, call types.ToolCall) (bool, string) {
	if s.approval == nil {
		return true, ""
	}

	return s.approval.Request(
		ctx,
		call.Name,
		toolApprovalDescription(call),
	)
}

func (s *Soul) lookupExecutor(name string) (ToolExecutor, bool) {
	if s.registry == nil {
		return nil, false
	}
	return s.registry.Executor(strings.TrimSpace(name))
}

func (s *Soul) buildChatMessages() []llm.Message {
	history := s.context.Messages()
	messages := make([]llm.Message, 0, len(history)+1)
	if s.systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role: "system",
			Content: types.ContentParts{
				types.TextPart{Text: s.systemPrompt},
			},
		})
	}

	for i := range history {
		messages = append(messages, llm.Message{
			Role:       string(history[i].Role),
			Content:    cloneContentParts(history[i].Content),
			ToolCalls:  cloneToolCalls(history[i].ToolCalls),
			ToolCallID: strings.TrimSpace(history[i].ToolCallID),
		})
	}
	return messages
}

func (s *Soul) toolDefinitions() []llm.ToolDefinition {
	if s.registry == nil {
		return nil
	}
	defs := s.registry.Definitions()
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(defs))
	copy(out, defs)
	return out
}

func (s *Soul) emit(msg wire.WireMessage) error {
	if s.wire == nil {
		return nil
	}
	if err := s.wire.Emit(msg); err != nil {
		return fmt.Errorf("soul wire emit: %w", err)
	}
	return nil
}

func normalizeToolCall(call types.ToolCall, index int) types.ToolCall {
	call.ID = strings.TrimSpace(call.ID)
	if call.ID == "" {
		call.ID = fmt.Sprintf("tool_call_%d", index)
	}
	call.Name = strings.TrimSpace(call.Name)
	return call
}

func textFromContentPart(part types.ContentPart) string {
	switch typed := part.(type) {
	case types.TextPart:
		return typed.Text
	case *types.TextPart:
		if typed == nil {
			return ""
		}
		return typed.Text
	default:
		return ""
	}
}

func toolErrorResult(call types.ToolCall, message string) types.ToolResult {
	return types.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"error": message,
			},
		},
		IsError: true,
	}
}

func toolRejectedResult(call types.ToolCall, feedback string) types.ToolResult {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return toolErrorResult(call, "tool call rejected")
	}
	return toolErrorResult(call, "tool call rejected: "+feedback)
}

func toolApprovalDescription(call types.ToolCall) string {
	encodedArgs, err := json.Marshal(call.Arguments)
	if err != nil {
		return fmt.Sprintf("tool=%s args=%v", call.Name, call.Arguments)
	}

	argumentSummary := strings.TrimSpace(string(encodedArgs))
	if argumentSummary == "" || argumentSummary == "null" {
		return fmt.Sprintf("tool=%s", call.Name)
	}

	return fmt.Sprintf("tool=%s args=%s", call.Name, argumentSummary)
}

func cloneContentParts(parts types.ContentParts) types.ContentParts {
	if len(parts) == 0 {
		return nil
	}
	out := make(types.ContentParts, len(parts))
	copy(out, parts)
	return out
}

func cloneToolCalls(toolCalls []types.ToolCall) []types.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]types.ToolCall, len(toolCalls))
	copy(out, toolCalls)
	return out
}
