package soul

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

var errToolsAlreadyExecuted = errors.New("soul step: tools already executed")

func (s *Soul) step(ctx context.Context, turnID string) (StepResult, error) {
	if err := s.ensureReady(); err != nil {
		return StepResult{}, err
	}

	messages, err := s.buildChatMessages(ctx)
	if err != nil {
		return StepResult{}, err
	}

	stream, err := s.provider.ChatStream(ctx, llm.ChatRequest{
		Messages: messages,
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
			return nil, fmt.Errorf("%w: emit tool call result: %w", errToolsAlreadyExecuted, err)
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

func (s *Soul) buildChatMessages(ctx context.Context) ([]llm.Message, error) {
	history := s.context.Messages()
	history = normalizeHistory(history)
	if injected := s.runPreStepHooks(ctx, history); len(injected) > 0 {
		history = append(history, injected...)
		history = normalizeHistory(history)
	}

	messages := make([]llm.Message, 0, len(history)+1)
	systemPrompt, err := s.renderSystemPrompt()
	if err != nil {
		return nil, err
	}
	if systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role: "system",
			Content: types.ContentParts{
				types.TextPart{Text: systemPrompt},
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
	return messages, nil
}

func (s *Soul) runPreStepHooks(ctx context.Context, history []Message) []Message {
	hooks := s.preStepHooksSnapshot()
	if len(hooks) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	out := make([]Message, 0, 4)
	for i := range hooks {
		if hooks[i] == nil {
			continue
		}

		injected := hooks[i](ctx, cloneMessages(history))
		if len(injected) == 0 {
			continue
		}
		for j := range injected {
			msg := sanitizeHookMessage(injected[j])
			if msg == nil {
				continue
			}
			out = append(out, *msg)
		}
	}
	return out
}

func sanitizeHookMessage(msg Message) *Message {
	msg.Role = Role(strings.TrimSpace(string(msg.Role)))
	msg.ToolCallID = strings.TrimSpace(msg.ToolCallID)
	if err := validateMessage(msg); err != nil {
		return nil
	}
	msg.Content = cloneContentParts(msg.Content)
	msg.ToolCalls = cloneToolCalls(msg.ToolCalls)
	return &msg
}

func (s *Soul) renderSystemPrompt() (string, error) {
	prompt := strings.TrimSpace(s.systemPrompt)
	if prompt == "" {
		return "", nil
	}
	if !strings.Contains(prompt, "{{") {
		return prompt, nil
	}

	tpl, err := template.New("system_prompt").Option("missingkey=zero").Parse(prompt)
	if err != nil {
		return "", fmt.Errorf("soul step: parse system prompt template: %w", err)
	}

	var buffer bytes.Buffer
	if err := tpl.Execute(&buffer, s.resolveTemplateData()); err != nil {
		return "", fmt.Errorf("soul step: render system prompt template: %w", err)
	}
	return strings.TrimSpace(buffer.String()), nil
}

func normalizeHistory(history []Message) []Message {
	if len(history) == 0 {
		return nil
	}

	out := make([]Message, 0, len(history))
	for i := range history {
		current := cloneMessage(history[i])
		if len(out) == 0 {
			out = append(out, current)
			continue
		}

		last := &out[len(out)-1]
		if !canMergeMessages(*last, current) {
			out = append(out, current)
			continue
		}

		last.Content = append(cloneContentParts(last.Content), cloneContentParts(current.Content)...)
		last.ToolCalls = append(cloneToolCalls(last.ToolCalls), cloneToolCalls(current.ToolCalls)...)
	}
	return out
}

func canMergeMessages(left, right Message) bool {
	if left.Role != right.Role {
		return false
	}

	leftToolCallID := strings.TrimSpace(left.ToolCallID)
	rightToolCallID := strings.TrimSpace(right.ToolCallID)
	if leftToolCallID != rightToolCallID {
		return false
	}

	if left.Role == RoleTool && leftToolCallID == "" {
		return false
	}
	if left.Role != RoleTool && leftToolCallID != "" {
		return false
	}
	return true
}

func cloneMessage(message Message) Message {
	return Message{
		Role:       message.Role,
		Content:    cloneContentParts(message.Content),
		ToolCalls:  cloneToolCalls(message.ToolCalls),
		ToolCallID: strings.TrimSpace(message.ToolCallID),
	}
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
