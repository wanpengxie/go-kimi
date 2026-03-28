package soul

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
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
	if len(result.Content) != 2 {
		t.Fatalf("len(result.Content) = %d, want 2", len(result.Content))
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
	if err != nil {
		t.Fatalf("Run() error = %v", err)
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

type scriptedChatProvider struct {
	streams [][]llm.ChatEvent
	chatErr error

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
	events := []llm.ChatEvent{{Done: true}}
	if index < len(p.streams) {
		events = p.streams[index]
	}
	p.mu.Unlock()

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
