package soul

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func TestSoulStepExecutesToolCallsAndEmitsEvents(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	if err := ctxStore.Append(Message{
		Role: RoleUser,
		Content: types.ContentParts{
			types.TextPart{Text: "hello"},
		},
	}); err != nil {
		t.Fatalf("Append(user) error = %v", err)
	}

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "Hel"}},
				{Delta: types.TextPart{Text: "lo"}},
				{ToolCall: &types.ToolCall{
					ID:   "call-1",
					Name: "search",
					Arguments: map[string]any{
						"q": "go",
					},
				}},
				{
					Usage: &types.TokenUsage{
						InputTokens:  2,
						OutputTokens: 3,
						TotalTokens:  5,
					},
					Done: true,
				},
			},
		},
	}

	var gotToolCall types.ToolCall
	registry := mockRegistry{
		definitions: []llm.ToolDefinition{
			{Name: "search"},
		},
		executors: map[string]ToolExecutor{
			"search": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				gotToolCall = call
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"ok": true},
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "system prompt")

	result, err := s.step(context.Background(), "turn-1")
	if err != nil {
		t.Fatalf("step() error = %v", err)
	}
	// Chunked TextDeltas ("Hel"+"lo") collapse into a single TextPart at
	// the result.Content level so context.jsonl is not polluted with
	// per-token TextPart entries.
	if len(result.Content) != 1 {
		t.Fatalf("len(result.Content) = %d, want 1 (collapsed)", len(result.Content))
	}
	if text, ok := result.Content[0].(types.TextPart); !ok || text.Text != "Hello" {
		t.Fatalf("result.Content[0] = %#v, want TextPart{Hello}", result.Content[0])
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(result.ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("len(result.ToolResults) = %d, want 1", len(result.ToolResults))
	}
	if result.Usage.TotalTokens != 5 {
		t.Fatalf("result.Usage.TotalTokens = %d, want 5", result.Usage.TotalTokens)
	}
	if gotToolCall.ID != "call-1" || gotToolCall.Name != "search" {
		t.Fatalf("executor call = %#v, want id=call-1 name=search", gotToolCall)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider call count = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 2 {
		t.Fatalf("request message count = %d, want 2 (system + user)", len(requests[0].Messages))
	}
	if requests[0].Messages[0].Role != "system" {
		t.Fatalf("request first role = %q, want system", requests[0].Messages[0].Role)
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "search" {
		t.Fatalf("request tools = %#v, want one search tool", requests[0].Tools)
	}

	events := drainWireMessages(wireCh)
	if len(events) != 4 {
		t.Fatalf("wire event count = %d, want 4", len(events))
	}
	text1, ok := events[0].(wire.TextDelta)
	if !ok || text1.Delta != "Hel" || text1.TurnID != "turn-1" {
		t.Fatalf("event[0] = %#v, want wire.TextDelta{turn-1, Hel}", events[0])
	}
	text2, ok := events[1].(wire.TextDelta)
	if !ok || text2.Delta != "lo" || text2.TurnID != "turn-1" {
		t.Fatalf("event[1] = %#v, want wire.TextDelta{turn-1, lo}", events[1])
	}
	reqEvt, ok := events[2].(wire.ToolCallRequest)
	if !ok || reqEvt.ToolCall.ID != "call-1" {
		t.Fatalf("event[2] = %#v, want ToolCallRequest(call-1)", events[2])
	}
	resEvt, ok := events[3].(wire.ToolCallResult)
	if !ok || resEvt.Result.ToolCallID != "call-1" || resEvt.Result.Name != "search" {
		t.Fatalf("event[3] = %#v, want ToolCallResult(call-1/search)", events[3])
	}
}

func TestSoulRunStopsWhenNoToolCalls(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "done"}},
				{
					Usage: &types.TokenUsage{
						InputTokens:  1,
						OutputTokens: 1,
						TotalTokens:  2,
					},
					Done: true,
				},
			},
		},
	}
	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")

	result, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider call count = %d, want 1", provider.CallCount())
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("len(result.ToolCalls) = %d, want 0", len(result.ToolCalls))
	}

	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("context message count = %d, want 2", len(messages))
	}
	if messages[0].Role != RoleUser || messages[1].Role != RoleAssistant {
		t.Fatalf("context roles = %#v, want [user assistant]", []Role{messages[0].Role, messages[1].Role})
	}

	events := drainWireMessages(wireCh)
	if len(events) != 3 {
		t.Fatalf("wire event count = %d, want 3", len(events))
	}
	if _, ok := events[0].(wire.TurnBegin); !ok {
		t.Fatalf("event[0] = %T, want wire.TurnBegin", events[0])
	}
	if delta, ok := events[1].(wire.TextDelta); !ok || delta.Delta != "done" {
		t.Fatalf("event[1] = %#v, want wire.TextDelta(done)", events[1])
	}
	end, ok := events[2].(wire.TurnEnd)
	if !ok {
		t.Fatalf("event[2] = %T, want wire.TurnEnd", events[2])
	}
	if end.StopReason != "stop" {
		t.Fatalf("TurnEnd.StopReason = %q, want stop", end.StopReason)
	}
	if end.Usage == nil || end.Usage.TotalTokens != 2 {
		t.Fatalf("TurnEnd.Usage = %#v, want total_tokens=2", end.Usage)
	}
}

func TestSoulRunStopsAtMaxSteps(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "step-1"}},
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "echo", Arguments: map[string]any{"v": 1}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "step-2"}},
				{ToolCall: &types.ToolCall{ID: "call-2", Name: "echo", Arguments: map[string]any{"v": 2}}},
				{Done: true},
			},
		},
	}
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"echo": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "ok-" + call.ID,
					},
				}, nil
			}),
		},
	}
	wireCh := make(chan wire.WireMessage, 32)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetMaxSteps(2)

	result, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "loop"},
	})
	if !errors.Is(err, kimierrors.ErrMaxStepsReached) {
		t.Fatalf("Run() error = %v, want ErrMaxStepsReached", err)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request message count = %d, want 3", len(requests[1].Messages))
	}
	if requests[1].Messages[1].Role != "assistant" {
		t.Fatalf("second request assistant role = %q, want assistant", requests[1].Messages[1].Role)
	}
	if len(requests[1].Messages[1].ToolCalls) != 1 || requests[1].Messages[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("second request assistant tool_calls = %#v, want one call-1", requests[1].Messages[1].ToolCalls)
	}
	if requests[1].Messages[2].Role != "tool" || requests[1].Messages[2].ToolCallID != "call-1" {
		t.Fatalf("second request tool message = %#v, want role=tool tool_call_id=call-1", requests[1].Messages[2])
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-2" {
		t.Fatalf("final step tool calls = %#v, want call-2", result.ToolCalls)
	}

	messages := ctxStore.Messages()
	if len(messages) != 5 {
		t.Fatalf("context message count = %d, want 5", len(messages))
	}
	gotRoles := []Role{
		messages[0].Role,
		messages[1].Role,
		messages[2].Role,
		messages[3].Role,
		messages[4].Role,
	}
	wantRoles := []Role{RoleUser, RoleAssistant, RoleTool, RoleAssistant, RoleTool}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("context roles = %#v, want %#v", gotRoles, wantRoles)
	}
	if messages[2].ToolCallID != "call-1" || messages[4].ToolCallID != "call-2" {
		t.Fatalf("tool message ids = [%q %q], want [call-1 call-2]", messages[2].ToolCallID, messages[4].ToolCallID)
	}

	events := drainWireMessages(wireCh)
	if len(events) != 8 {
		t.Fatalf("wire event count = %d, want 8", len(events))
	}
	typesByIndex := []string{
		"turn_begin",
		"text_delta",
		"tool_call_request",
		"tool_call_result",
		"text_delta",
		"tool_call_request",
		"tool_call_result",
		"turn_end",
	}
	for i := range events {
		gotType := ""
		switch events[i].(type) {
		case wire.TurnBegin:
			gotType = "turn_begin"
		case wire.TextDelta:
			gotType = "text_delta"
		case wire.ToolCallRequest:
			gotType = "tool_call_request"
		case wire.ToolCallResult:
			gotType = "tool_call_result"
		case wire.TurnEnd:
			gotType = "turn_end"
		}
		if gotType != typesByIndex[i] {
			t.Fatalf("event[%d] type = %q, want %q", i, gotType, typesByIndex[i])
		}
	}
	end, ok := events[7].(wire.TurnEnd)
	if !ok {
		t.Fatalf("event[7] = %T, want wire.TurnEnd", events[7])
	}
	if end.StopReason != "max_steps" {
		t.Fatalf("TurnEnd.StopReason = %q, want max_steps", end.StopReason)
	}
}

func TestSoulRunRetriesTransientStepError(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "retry success"}},
				{Done: true},
			},
		},
		streamErrs: []error{
			errors.New("temporary stream failure"),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 1,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	})

	result, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider.CallCount() = %d, want 2", provider.CallCount())
	}
	if got := strings.TrimSpace(contentPartsText(result.Content)); got != "retry success" {
		t.Fatalf("result content = %q, want %q", got, "retry success")
	}

	events := drainWireMessages(wireCh)
	hasRetryEvent := false
	for i := range events {
		interrupted, ok := events[i].(wire.StepInterrupted)
		if !ok {
			continue
		}
		hasRetryEvent = true
		if !strings.Contains(interrupted.Reason, "retry 1/1") {
			t.Fatalf("StepInterrupted.Reason = %q, want contains retry 1/1", interrupted.Reason)
		}
	}
	if !hasRetryEvent {
		t.Fatalf("wire events missing StepInterrupted retry event: %#v", events)
	}
}

func TestSoulRunDoesNotRetryAfterToolResultEmitFailure(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "write_once"}},
				{Done: true},
			},
		},
	}

	toolExecCount := 0
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"write_once": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				toolExecCount++
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"ok": true},
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	toolResultEmitAttempts := 0
	emitter := emitterFunc(func(msg wire.WireMessage) error {
		if _, ok := msg.(wire.ToolCallResult); ok {
			toolResultEmitAttempts++
			if toolResultEmitAttempts == 1 {
				return errors.New("injected tool result emit failure")
			}
		}
		wireCh <- msg
		return nil
	})

	s := NewSoul(provider, ctxStore, registry, emitter, "")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	})

	_, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "run tool"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want emit failure")
	}
	if !errors.Is(err, errToolsAlreadyExecuted) {
		t.Fatalf("Run() error = %v, want errors.Is(errToolsAlreadyExecuted)", err)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider.CallCount() = %d, want 1", provider.CallCount())
	}
	if toolExecCount != 1 {
		t.Fatalf("tool execution count = %d, want 1", toolExecCount)
	}

	events := drainWireMessages(wireCh)
	for i := range events {
		if _, ok := events[i].(wire.StepInterrupted); ok {
			t.Fatalf("unexpected StepInterrupted event when tool result emit failed: %#v", events[i])
		}
	}
}

func TestSoulRunDoesNotRetryContextCanceled(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streamErrs: []error{context.Canceled},
	}
	wireCh := make(chan wire.WireMessage, 16)

	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	})

	_, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want errors.Is(context.Canceled)", err)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider.CallCount() = %d, want 1", provider.CallCount())
	}

	events := drainWireMessages(wireCh)
	for i := range events {
		if _, ok := events[i].(wire.StepInterrupted); ok {
			t.Fatalf("unexpected StepInterrupted event for context.Canceled: %#v", events[i])
		}
	}
}

func TestSoulRunRetryExhaustedErrorWrapsLastError(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	transientErr := errors.New("temporary stream failure")
	provider := &scriptedChatProvider{
		streamErrs: []error{transientErr, transientErr, transientErr},
	}
	wireCh := make(chan wire.WireMessage, 16)

	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	})

	_, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want retry exhausted error")
	}
	if !errors.Is(err, transientErr) {
		t.Fatalf("Run() error = %v, want errors.Is(transientErr)", err)
	}
	if !strings.Contains(err.Error(), "step 1 failed after 2 retries") {
		t.Fatalf("Run() error = %q, want contains %q", err.Error(), "step 1 failed after 2 retries")
	}
	if provider.CallCount() != 3 {
		t.Fatalf("provider.CallCount() = %d, want 3", provider.CallCount())
	}

	events := drainWireMessages(wireCh)
	retryEvents := 0
	for i := range events {
		if _, ok := events[i].(wire.StepInterrupted); ok {
			retryEvents++
		}
	}
	if retryEvents != 2 {
		t.Fatalf("StepInterrupted count = %d, want 2", retryEvents)
	}
}

func TestSoulRunRetryBackoffAppliesMinimumDelay(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Done: true},
			},
		},
		streamErrs: []error{
			errors.New("temporary stream failure"),
		},
	}

	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.NoopEmitter{}, "")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 1,
		BaseDelay:  30 * time.Millisecond,
		MaxDelay:   30 * time.Millisecond,
	})

	started := time.Now()
	_, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider.CallCount() = %d, want 2", provider.CallCount())
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("retry elapsed = %v, want >= 25ms", elapsed)
	}
}

func TestSoulRunDoesNotRetrySystemPromptTemplateParseError(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Done: true},
			},
		},
	}
	wireCh := make(chan wire.WireMessage, 16)

	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "{{.WorkDir")
	s.SetStepRetryConfig(StepRetryConfig{
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	})

	_, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want template parse error")
	}
	if !strings.Contains(err.Error(), "parse system prompt template") {
		t.Fatalf("Run() error = %q, want contains parse system prompt template", err.Error())
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider.CallCount() = %d, want 0", provider.CallCount())
	}

	events := drainWireMessages(wireCh)
	for i := range events {
		if _, ok := events[i].(wire.StepInterrupted); ok {
			t.Fatalf("unexpected StepInterrupted event for template parse error: %#v", events[i])
		}
	}
}

func TestSoulRunAppliesSteerInputBetweenSteps(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "wait"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "final response"}},
				{Done: true},
			},
		},
	}

	gate := make(chan struct{})
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"wait": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				<-gate
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"ok": true},
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 32)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")

	type runOutcome struct {
		result StepResult
		err    error
	}
	outcomeCh := make(chan runOutcome, 1)
	go func() {
		result, err := s.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "first input"},
		})
		outcomeCh <- runOutcome{result: result, err: err}
	}()

	deadline := time.Now().Add(time.Second)
	for provider.CallCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if provider.CallCount() < 1 {
		t.Fatal("provider never started first step")
	}

	steerCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Steer(steerCtx, "steer input from user"); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	close(gate)

	var outcome runOutcome
	select {
	case outcome = <-outcomeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run() completion")
	}
	if outcome.err != nil {
		t.Fatalf("Run() error = %v", outcome.err)
	}
	if got := strings.TrimSpace(contentPartsText(outcome.result.Content)); got != "final response" {
		t.Fatalf("result content = %q, want %q", got, "final response")
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	hasSteerMessage := false
	for i := range requests[1].Messages {
		if requests[1].Messages[i].Role != "user" {
			continue
		}
		if strings.Contains(contentPartsText(requests[1].Messages[i].Content), "steer input from user") {
			hasSteerMessage = true
			break
		}
	}
	if !hasSteerMessage {
		t.Fatalf("second request missing steer message: %#v", requests[1].Messages)
	}

	events := drainWireMessages(wireCh)
	hasSteerEvent := false
	for i := range events {
		steer, ok := events[i].(wire.SteerInput)
		if !ok {
			continue
		}
		hasSteerEvent = true
		if steer.Text != "steer input from user" {
			t.Fatalf("SteerInput.Text = %q, want %q", steer.Text, "steer input from user")
		}
	}
	if !hasSteerEvent {
		t.Fatalf("wire events missing SteerInput: %#v", events)
	}

	messages := ctxStore.Messages()
	if len(messages) != 5 {
		t.Fatalf("context message count = %d, want 5", len(messages))
	}
}

func TestSoulSteerRejectsWhenNoActiveTurn(t *testing.T) {
	t.Parallel()

	s := NewSoul(&scriptedChatProvider{}, NewSoulContext(t.TempDir()), mockRegistry{}, wire.NoopEmitter{}, "")
	err := s.Steer(context.Background(), "steer-without-turn")
	if err == nil {
		t.Fatal("Steer() error = nil, want no active turn error")
	}
	if !strings.Contains(err.Error(), "no active turn") {
		t.Fatalf("Steer() error = %q, want contains %q", err.Error(), "no active turn")
	}
}

func TestSoulSteerQueueFullRespectsContextDeadline(t *testing.T) {
	t.Parallel()

	s := NewSoul(&scriptedChatProvider{}, NewSoulContext(t.TempDir()), mockRegistry{}, wire.NoopEmitter{}, "")
	s.beginTurnRuntime("turn-queue-full")

	for i := 0; i < cap(s.steerCh); i++ {
		s.steerCh <- steerRequest{
			TurnID:   "turn-queue-full",
			Text:     "occupied",
			Priority: "normal",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := s.Steer(ctx, "new steer input")
	if err == nil {
		t.Fatal("Steer() error = nil, want deadline exceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Steer() error = %v, want errors.Is(context.DeadlineExceeded)", err)
	}
}

func TestSoulConsumeSteerInputsSkipsInvalidAndConsumesAllMatching(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	wireCh := make(chan wire.WireMessage, 8)
	s := NewSoul(&scriptedChatProvider{}, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	s.beginTurnRuntime("turn-steer-consume")

	s.steerCh <- steerRequest{TurnID: "other-turn", Text: "ignore-me", Priority: "normal"}
	s.steerCh <- steerRequest{TurnID: "turn-steer-consume", Text: "   ", Priority: "normal"}
	s.steerCh <- steerRequest{TurnID: "turn-steer-consume", Text: "first steer", Priority: "normal"}
	s.steerCh <- steerRequest{TurnID: "turn-steer-consume", Text: "second steer", Priority: "normal"}

	consumed, err := s.consumeSteerInputs("turn-steer-consume")
	if err != nil {
		t.Fatalf("consumeSteerInputs() error = %v", err)
	}
	if !consumed {
		t.Fatal("consumeSteerInputs() consumed = false, want true")
	}

	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("context message count = %d, want 2", len(messages))
	}
	if got := contentPartsText(messages[0].Content); strings.TrimSpace(got) != "first steer" {
		t.Fatalf("messages[0] = %q, want %q", got, "first steer")
	}
	if got := contentPartsText(messages[1].Content); strings.TrimSpace(got) != "second steer" {
		t.Fatalf("messages[1] = %q, want %q", got, "second steer")
	}

	events := drainWireMessages(wireCh)
	if len(events) != 2 {
		t.Fatalf("wire event count = %d, want 2", len(events))
	}
	first, ok := events[0].(wire.SteerInput)
	if !ok || strings.TrimSpace(first.Text) != "first steer" {
		t.Fatalf("event[0] = %#v, want first steer wire.SteerInput", events[0])
	}
	second, ok := events[1].(wire.SteerInput)
	if !ok || strings.TrimSpace(second.Text) != "second steer" {
		t.Fatalf("event[1] = %#v, want second steer wire.SteerInput", events[1])
	}

	consumed, err = s.consumeSteerInputs("turn-steer-consume")
	if err != nil {
		t.Fatalf("second consumeSteerInputs() error = %v", err)
	}
	if consumed {
		t.Fatal("second consumeSteerInputs() consumed = true, want false")
	}
}

func TestSoulBuildChatMessagesTemplateAndHookNormalization(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "done"}},
				{Done: true},
			},
		},
	}

	s := NewSoul(
		provider,
		ctxStore,
		mockRegistry{},
		wire.NoopEmitter{},
		"workdir={{.WorkDir}};skills={{.Skills}};plan={{.PlanMode}};slug={{.PlanSlug}}",
	)
	s.ClearPreStepHooks()
	s.AddPreStepHook(func(_ context.Context, _ []Message) []Message {
		return []Message{
			{
				Role: RoleUser,
				Content: types.ContentParts{
					types.TextPart{Text: "hook detail"},
				},
			},
		}
	})
	s.SetSystemPromptTemplateData(SystemPromptTemplateData{
		WorkDir: "/tmp/worktree",
		Skills:  "alpha,beta",
	})
	s.SetPlanModeState(PlanModeState{
		Active: true,
		Slug:   "plan-2026",
	})

	if _, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "base input"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 2 {
		t.Fatalf("request message count = %d, want 2", len(requests[0].Messages))
	}
	systemPrompt := contentPartsText(requests[0].Messages[0].Content)
	if !strings.Contains(systemPrompt, "workdir=/tmp/worktree") {
		t.Fatalf("system prompt = %q, want contains workdir", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "skills=alpha,beta") {
		t.Fatalf("system prompt = %q, want contains skills", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "plan=true") || !strings.Contains(systemPrompt, "slug=plan-2026") {
		t.Fatalf("system prompt = %q, want contains plan fields", systemPrompt)
	}

	userMsg := requests[0].Messages[1]
	if userMsg.Role != "user" {
		t.Fatalf("request user role = %q, want user", userMsg.Role)
	}
	// normalizeHistory now collapses adjacent TextPart runs across merged
	// same-role messages — base + hook user text condense into one
	// TextPart with both substrings concatenated.
	if len(userMsg.Content) != 1 {
		t.Fatalf("normalized user content parts = %d, want 1 (collapsed)", len(userMsg.Content))
	}
	if got := contentPartsText(userMsg.Content); !strings.Contains(got, "base input") || !strings.Contains(got, "hook detail") {
		t.Fatalf("user content = %q, want merged base+hook", got)
	}
}

func TestSoulPlanModeHookInjectsReminder(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "ok"}},
				{Done: true},
			},
		},
	}

	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.NoopEmitter{}, "")
	s.SetPlanModeState(PlanModeState{
		Active:    true,
		SessionID: "session-61",
		Slug:      "plan-61",
	})

	if _, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "hello"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	hasPlanReminder := false
	for i := range requests[0].Messages {
		text := contentPartsText(requests[0].Messages[i].Content)
		if strings.Contains(text, "Plan mode is active") && strings.Contains(text, "plan-61") {
			hasPlanReminder = true
			break
		}
	}
	if !hasPlanReminder {
		t.Fatalf("request messages missing plan reminder: %#v", requests[0].Messages)
	}
}

func TestSoulPlanModeHookReminderThrottlesEveryThreeSteps(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "echo"}},
				{Done: true},
			},
			{
				{ToolCall: &types.ToolCall{ID: "call-2", Name: "echo"}},
				{Done: true},
			},
			{
				{ToolCall: &types.ToolCall{ID: "call-3", Name: "echo"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "done"}},
				{Done: true},
			},
		},
	}
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"echo": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value:      types.ToolReturnValue{Value: "ok"},
				}, nil
			}),
		},
	}

	s := NewSoul(provider, ctxStore, registry, wire.NoopEmitter{}, "")
	s.SetPlanModeState(PlanModeState{
		Active:    true,
		SessionID: "session-throttle",
		Slug:      "plan-throttle",
	})

	if _, err := s.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "run four steps"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 4 {
		t.Fatalf("provider request count = %d, want 4", len(requests))
	}

	countReminder := func(messages []llm.Message) int {
		count := 0
		for i := range messages {
			text := contentPartsText(messages[i].Content)
			if strings.Contains(text, "Plan mode is active") && strings.Contains(text, "plan-throttle") {
				count++
			}
		}
		return count
	}

	if got := countReminder(requests[0].Messages); got != 1 {
		t.Fatalf("step-1 reminder count = %d, want 1", got)
	}
	if got := countReminder(requests[1].Messages); got != 0 {
		t.Fatalf("step-2 reminder count = %d, want 0", got)
	}
	if got := countReminder(requests[2].Messages); got != 0 {
		t.Fatalf("step-3 reminder count = %d, want 0", got)
	}
	if got := countReminder(requests[3].Messages); got != 1 {
		t.Fatalf("step-4 reminder count = %d, want 1", got)
	}
}

func TestSoulYoloHookInjectsReminderOncePerTurn(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "echo"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "step-2"}},
				{Done: true},
			},
		},
	}
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"echo": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"ok": true},
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetYolo(false)

	type runOutcome struct {
		result StepResult
		err    error
	}
	outcomeCh := make(chan runOutcome, 1)
	go func() {
		result, err := s.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "start"},
		})
		outcomeCh <- runOutcome{result: result, err: err}
	}()

	var approvalReq wire.ApprovalRequest
	gotApprovalReq := false
	deadline := time.After(time.Second)
	for !gotApprovalReq {
		select {
		case msg := <-wireCh:
			request, ok := msg.(wire.ApprovalRequest)
			if !ok {
				continue
			}
			approvalReq = request
			gotApprovalReq = true
		case <-deadline:
			t.Fatal("timeout waiting for approval request")
		}
	}

	if err := s.RespondApproval(approvalReq.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}

	select {
	case outcome := <-outcomeCh:
		if outcome.err != nil {
			t.Fatalf("Run() error = %v", outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run() completion")
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}

	hasReminder := func(messages []llm.Message) bool {
		for i := range messages {
			if !strings.Contains(contentPartsText(messages[i].Content), "Tool approvals are required in this turn") {
				continue
			}
			return true
		}
		return false
	}
	if !hasReminder(requests[0].Messages) {
		t.Fatalf("first request missing yolo reminder: %#v", requests[0].Messages)
	}
	if hasReminder(requests[1].Messages) {
		t.Fatalf("second request unexpectedly repeats yolo reminder: %#v", requests[1].Messages)
	}
}

func TestSoulStepToolExecutorErrorBecomesToolResult(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "call-1", Name: "boom"}},
				{Done: true},
			},
		},
	}
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"boom": executorFunc(func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
				return types.ToolResult{}, errors.New("explode")
			}),
		},
	}
	wireCh := make(chan wire.WireMessage, 8)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")

	result, err := s.step(context.Background(), "turn-err")
	if err != nil {
		t.Fatalf("step() error = %v", err)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("len(result.ToolResults) = %d, want 1", len(result.ToolResults))
	}
	if !result.ToolResults[0].IsError {
		t.Fatalf("ToolResult.IsError = %v, want true", result.ToolResults[0].IsError)
	}
	value, ok := result.ToolResults[0].Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("ToolResult.Value type = %T, want map[string]any", result.ToolResults[0].Value.Value)
	}
	if !strings.Contains(value["error"].(string), "explode") {
		t.Fatalf("ToolResult error payload = %#v, want explode", value)
	}
}

func TestSoulStepApprovalRejectSkipsExecutor(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{
					ID:   "call-1",
					Name: "search",
					Arguments: map[string]any{
						"q": "go",
					},
				}},
				{Done: true},
			},
		},
	}

	executed := false
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"search": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				executed = true
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"ok": true},
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetYolo(false)

	type stepOutcome struct {
		result StepResult
		err    error
	}
	outcomeCh := make(chan stepOutcome, 1)
	go func() {
		result, err := s.step(context.Background(), "turn-approval-reject")
		outcomeCh <- stepOutcome{result: result, err: err}
	}()

	events := make([]wire.WireMessage, 0, 4)
	var approvalReq wire.ApprovalRequest
	gotApproval := false
	deadline := time.After(time.Second)
	for !gotApproval {
		select {
		case msg := <-wireCh:
			if msg == nil {
				t.Fatal("received nil wire message while waiting approval request")
			}
			events = append(events, msg)
			if request, ok := msg.(wire.ApprovalRequest); ok {
				approvalReq = request
				gotApproval = true
			}
		case <-deadline:
			t.Fatal("timeout waiting for approval request wire message")
		}
	}

	if err := s.RespondApproval(approvalReq.ID, ApprovalReject, "blocked by policy"); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}

	var outcome stepOutcome
	select {
	case outcome = <-outcomeCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for step completion")
	}
	if outcome.err != nil {
		t.Fatalf("step() error = %v", outcome.err)
	}
	if executed {
		t.Fatalf("tool executor executed = true, want false")
	}
	if len(outcome.result.ToolResults) != 1 {
		t.Fatalf("len(result.ToolResults) = %d, want 1", len(outcome.result.ToolResults))
	}
	if !outcome.result.ToolResults[0].IsError {
		t.Fatalf("ToolResult.IsError = false, want true")
	}
	value, ok := outcome.result.ToolResults[0].Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("ToolResult.Value type = %T, want map[string]any", outcome.result.ToolResults[0].Value.Value)
	}
	errorText, _ := value["error"].(string)
	if !strings.Contains(errorText, "rejected") || !strings.Contains(errorText, "blocked by policy") {
		t.Fatalf("ToolResult error payload = %#v, want rejected + blocked by policy", value)
	}

	events = append(events, drainWireMessages(wireCh)...)
	if len(events) != 3 {
		t.Fatalf("wire event count = %d, want 3", len(events))
	}
	if _, ok := events[0].(wire.ToolCallRequest); !ok {
		t.Fatalf("event[0] = %T, want wire.ToolCallRequest", events[0])
	}
	if _, ok := events[1].(wire.ApprovalRequest); !ok {
		t.Fatalf("event[1] = %T, want wire.ApprovalRequest", events[1])
	}
	if _, ok := events[2].(wire.ToolCallResult); !ok {
		t.Fatalf("event[2] = %T, want wire.ToolCallResult", events[2])
	}
}

func TestSoulStepPlanModeAutoApprovesPlanFileMutation(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	planFile := filepath.Join(t.TempDir(), "plan.md")

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{
					ID:   "call-1",
					Name: "write_file",
					Arguments: map[string]any{
						"path":    planFile,
						"content": "# plan",
					},
				}},
				{Done: true},
			},
		},
	}

	executed := false
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"write_file": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				executed = true
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "ok",
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetYolo(false)
	s.SetPlanModeState(PlanModeState{
		Active:   true,
		PlanFile: planFile,
	})

	result, err := s.step(context.Background(), "turn-plan-auto-approve")
	if err != nil {
		t.Fatalf("step() error = %v", err)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("len(result.ToolResults) = %d, want 1", len(result.ToolResults))
	}
	if result.ToolResults[0].IsError {
		t.Fatalf("ToolResult.IsError = %v, want false", result.ToolResults[0].IsError)
	}
	if !executed {
		t.Fatalf("tool executor executed = false, want true")
	}

	events := drainWireMessages(wireCh)
	if len(events) != 2 {
		t.Fatalf("wire event count = %d, want 2", len(events))
	}
	if _, ok := events[0].(wire.ToolCallRequest); !ok {
		t.Fatalf("event[0] = %T, want wire.ToolCallRequest", events[0])
	}
	if _, ok := events[1].(wire.ToolCallResult); !ok {
		t.Fatalf("event[1] = %T, want wire.ToolCallResult", events[1])
	}
}

func TestSoulStepPlanModeAutoApprovesStrReplacePlanFileMutation(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	planFile := filepath.Join(t.TempDir(), "plan.md")

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{
					ID:   "call-1",
					Name: "str_replace",
					Arguments: map[string]any{
						"path":       planFile,
						"old_string": "phase 1",
						"new_string": "phase 2",
					},
				}},
				{Done: true},
			},
		},
	}

	executed := false
	registry := mockRegistry{
		executors: map[string]ToolExecutor{
			"str_replace": executorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				executed = true
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "ok",
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	s := NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	s.SetYolo(false)
	s.SetPlanModeState(PlanModeState{
		Active:   true,
		PlanFile: planFile,
	})

	result, err := s.step(context.Background(), "turn-plan-auto-approve-str-replace")
	if err != nil {
		t.Fatalf("step() error = %v", err)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("len(result.ToolResults) = %d, want 1", len(result.ToolResults))
	}
	if result.ToolResults[0].IsError {
		t.Fatalf("ToolResult.IsError = %v, want false", result.ToolResults[0].IsError)
	}
	if !executed {
		t.Fatalf("tool executor executed = false, want true")
	}

	events := drainWireMessages(wireCh)
	if len(events) != 2 {
		t.Fatalf("wire event count = %d, want 2", len(events))
	}
	if _, ok := events[0].(wire.ToolCallRequest); !ok {
		t.Fatalf("event[0] = %T, want wire.ToolCallRequest", events[0])
	}
	if _, ok := events[1].(wire.ToolCallResult); !ok {
		t.Fatalf("event[1] = %T, want wire.ToolCallResult", events[1])
	}
}

type scriptedChatProvider struct {
	streams    [][]llm.ChatEvent
	chatErr    error
	streamErrs []error

	mu       sync.Mutex
	requests []llm.ChatRequest
	calls    int
}

func (p *scriptedChatProvider) ModelName() string {
	return "scripted"
}

func (p *scriptedChatProvider) WithModel(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedChatProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedChatProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	chatErr := p.chatErr
	p.mu.Unlock()
	if chatErr != nil {
		return nil, chatErr
	}
	return &llm.ChatResponse{}, nil
}

func (p *scriptedChatProvider) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneChatRequest(req))
	index := p.calls
	p.calls++
	var streamErr error
	if index < len(p.streamErrs) {
		streamErr = p.streamErrs[index]
	}
	events := []llm.ChatEvent{{Done: true}}
	if index < len(p.streams) {
		events = p.streams[index]
	}
	p.mu.Unlock()
	if streamErr != nil {
		return nil, streamErr
	}

	ch := make(chan llm.ChatEvent, len(events))
	for i := range events {
		ch <- events[i]
	}
	close(ch)
	return ch, nil
}

func (p *scriptedChatProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]llm.ChatRequest, len(p.requests))
	for i := range p.requests {
		out[i] = cloneChatRequest(p.requests[i])
	}
	return out
}

func (p *scriptedChatProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type executorFunc func(ctx context.Context, call types.ToolCall) (types.ToolResult, error)

func (f executorFunc) Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error) {
	return f(ctx, call)
}

type emitterFunc func(msg wire.WireMessage) error

func (f emitterFunc) Emit(msg wire.WireMessage) error {
	return f(msg)
}

type mockRegistry struct {
	definitions []llm.ToolDefinition
	executors   map[string]ToolExecutor
}

func (r mockRegistry) Definitions() []llm.ToolDefinition {
	if len(r.definitions) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(r.definitions))
	copy(out, r.definitions)
	return out
}

func (r mockRegistry) Executor(name string) (ToolExecutor, bool) {
	if r.executors == nil {
		return nil, false
	}
	executor, ok := r.executors[name]
	return executor, ok
}

func cloneChatRequest(req llm.ChatRequest) llm.ChatRequest {
	out := req
	if len(req.Messages) > 0 {
		out.Messages = make([]llm.Message, len(req.Messages))
		copy(out.Messages, req.Messages)
	}
	if len(req.Tools) > 0 {
		out.Tools = make([]llm.ToolDefinition, len(req.Tools))
		copy(out.Tools, req.Tools)
	}
	return out
}

func drainWireMessages(ch <-chan wire.WireMessage) []wire.WireMessage {
	messages := make([]wire.WireMessage, 0, cap(ch))
	for {
		select {
		case message := <-ch:
			if message == nil {
				return messages
			}
			messages = append(messages, message)
		default:
			return messages
		}
	}
}
