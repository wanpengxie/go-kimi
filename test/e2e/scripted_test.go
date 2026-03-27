//go:build e2e

package e2e

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestScriptedSoulSingleTurn(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "hello from scripted soul"}},
				{
					Usage: &types.TokenUsage{
						InputTokens:  4,
						OutputTokens: 5,
						TotalTokens:  9,
					},
					Done: true,
				},
			},
		},
	}
	wireCh := make(chan wire.WireMessage, 16)
	engine := soul.NewSoul(provider, ctxStore, scriptedToolRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")

	input := types.ContentParts{types.TextPart{Text: "hello"}}
	result, err := engine.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "hello from scripted soul" {
		t.Fatalf("result text = %q, want %q", got, "hello from scripted soul")
	}

	if provider.CallCount() != 1 {
		t.Fatalf("provider call count = %d, want 1", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 1 || requests[0].Messages[0].Role != "user" {
		t.Fatalf("provider request messages = %#v, want single user message", requests[0].Messages)
	}

	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("context message count = %d, want 2", len(messages))
	}
	gotRoles := []soul.Role{messages[0].Role, messages[1].Role}
	wantRoles := []soul.Role{soul.RoleUser, soul.RoleAssistant}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("context roles = %#v, want %#v", gotRoles, wantRoles)
	}

	events := drainWireMessages(wireCh)
	if len(events) != 3 {
		t.Fatalf("wire event count = %d, want 3", len(events))
	}
	begin, ok := events[0].(wire.TurnBegin)
	if !ok {
		t.Fatalf("event[0] = %T, want wire.TurnBegin", events[0])
	}
	delta, ok := events[1].(wire.TextDelta)
	if !ok {
		t.Fatalf("event[1] = %T, want wire.TextDelta", events[1])
	}
	end, ok := events[2].(wire.TurnEnd)
	if !ok {
		t.Fatalf("event[2] = %T, want wire.TurnEnd", events[2])
	}
	if begin.TurnID == "" || begin.TurnID != delta.TurnID || begin.TurnID != end.TurnID {
		t.Fatalf("turn id mismatch: begin=%q delta=%q end=%q", begin.TurnID, delta.TurnID, end.TurnID)
	}
	if delta.Delta != "hello from scripted soul" {
		t.Fatalf("TextDelta.Delta = %q, want %q", delta.Delta, "hello from scripted soul")
	}
	if end.StopReason != "stop" {
		t.Fatalf("TurnEnd.StopReason = %q, want stop", end.StopReason)
	}
	if end.Usage == nil || end.Usage.TotalTokens != 9 {
		t.Fatalf("TurnEnd.Usage = %#v, want total_tokens=9", end.Usage)
	}
}

func TestScriptedSoulWithToolCall(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-1",
						Name: "echo",
						Arguments: map[string]any{
							"message": "hello tool",
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "tool call handled"}},
				{
					Usage: &types.TokenUsage{
						InputTokens:  10,
						OutputTokens: 3,
						TotalTokens:  13,
					},
					Done: true,
				},
			},
		},
	}

	toolCalls := make(chan types.ToolCall, 1)
	registry := scriptedToolRegistry{
		definitions: []llm.ToolDefinition{{Name: "echo"}},
		executors: map[string]soul.ToolExecutor{
			"echo": toolExecutorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				toolCalls <- call
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "tool-output:ok",
					},
				}, nil
			}),
		},
	}

	wireCh := make(chan wire.WireMessage, 32)
	engine := soul.NewSoul(provider, ctxStore, registry, wire.ChannelEmitter{Ch: wireCh}, "")
	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "please call echo"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "tool call handled" {
		t.Fatalf("result text = %q, want %q", got, "tool call handled")
	}

	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
	select {
	case call := <-toolCalls:
		if call.ID != "call-1" || call.Name != "echo" {
			t.Fatalf("executor call = %#v, want id=call-1 name=echo", call)
		}
	default:
		t.Fatal("expected tool executor invocation, got none")
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	gotRoles := []soul.Role{messages[0].Role, messages[1].Role, messages[2].Role, messages[3].Role}
	wantRoles := []soul.Role{soul.RoleUser, soul.RoleAssistant, soul.RoleTool, soul.RoleAssistant}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("context roles = %#v, want %#v", gotRoles, wantRoles)
	}
	if len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant tool_calls = %#v, want one call-1", messages[1].ToolCalls)
	}
	if messages[2].ToolCallID != "call-1" {
		t.Fatalf("tool message ToolCallID = %q, want call-1", messages[2].ToolCallID)
	}
	if got := strings.TrimSpace(textFromContentParts(messages[2].Content)); got != "tool-output:ok" {
		t.Fatalf("tool message content = %q, want %q", got, "tool-output:ok")
	}

	events := drainWireMessages(wireCh)
	if len(events) != 5 {
		t.Fatalf("wire event count = %d, want 5", len(events))
	}
	if _, ok := events[0].(wire.TurnBegin); !ok {
		t.Fatalf("event[0] = %T, want wire.TurnBegin", events[0])
	}
	req, ok := events[1].(wire.ToolCallRequest)
	if !ok || req.ToolCall.ID != "call-1" {
		t.Fatalf("event[1] = %#v, want ToolCallRequest(call-1)", events[1])
	}
	res, ok := events[2].(wire.ToolCallResult)
	if !ok || res.Result.ToolCallID != "call-1" {
		t.Fatalf("event[2] = %#v, want ToolCallResult(call-1)", events[2])
	}
	delta, ok := events[3].(wire.TextDelta)
	if !ok || delta.Delta != "tool call handled" {
		t.Fatalf("event[3] = %#v, want TextDelta(tool call handled)", events[3])
	}
	if _, ok := events[4].(wire.TurnEnd); !ok {
		t.Fatalf("event[4] = %T, want wire.TurnEnd", events[4])
	}
}

func TestScriptedApprovalFlow(t *testing.T) {
	t.Run("yolo", func(t *testing.T) {
		t.Parallel()

		scenario := newApprovalScenario(t)
		scenario.engine.SetYolo(true)

		result, err := scenario.engine.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "run tool"},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "approval flow completed" {
			t.Fatalf("result text = %q, want %q", got, "approval flow completed")
		}
		if scenario.executed.Load() != 1 {
			t.Fatalf("tool executed count = %d, want 1", scenario.executed.Load())
		}

		events := drainWireMessages(scenario.wireCh)
		if hasApprovalRequest(events) {
			t.Fatalf("unexpected approval request event in yolo mode: %#v", events)
		}
	})

	t.Run("approve", func(t *testing.T) {
		t.Parallel()

		scenario := newApprovalScenario(t)
		scenario.engine.SetYolo(false)

		outcomeCh := make(chan runOutcome, 1)
		go func() {
			result, err := scenario.engine.Run(context.Background(), types.ContentParts{
				types.TextPart{Text: "run tool with approval"},
			})
			outcomeCh <- runOutcome{result: result, err: err}
		}()

		request, events := waitForApprovalRequest(t, scenario.wireCh, 2*time.Second)
		if err := scenario.engine.RespondApproval(request.ID, soul.ApprovalApprove, ""); err != nil {
			t.Fatalf("RespondApproval(approve) error = %v", err)
		}

		outcome := waitRunOutcome(t, outcomeCh, 2*time.Second)
		if outcome.err != nil {
			t.Fatalf("Run() error = %v", outcome.err)
		}
		if scenario.executed.Load() != 1 {
			t.Fatalf("tool executed count = %d, want 1", scenario.executed.Load())
		}

		events = append(events, drainWireMessages(scenario.wireCh)...)
		if !hasApprovalRequest(events) {
			t.Fatalf("expected approval request event, got %#v", events)
		}
	})

	t.Run("reject", func(t *testing.T) {
		t.Parallel()

		scenario := newApprovalScenario(t)
		scenario.engine.SetYolo(false)

		outcomeCh := make(chan runOutcome, 1)
		go func() {
			result, err := scenario.engine.Run(context.Background(), types.ContentParts{
				types.TextPart{Text: "run tool with rejection"},
			})
			outcomeCh <- runOutcome{result: result, err: err}
		}()

		request, events := waitForApprovalRequest(t, scenario.wireCh, 2*time.Second)
		if err := scenario.engine.RespondApproval(request.ID, soul.ApprovalReject, "blocked by policy"); err != nil {
			t.Fatalf("RespondApproval(reject) error = %v", err)
		}

		outcome := waitRunOutcome(t, outcomeCh, 2*time.Second)
		if outcome.err != nil {
			t.Fatalf("Run() error = %v", outcome.err)
		}
		if scenario.executed.Load() != 0 {
			t.Fatalf("tool executed count = %d, want 0", scenario.executed.Load())
		}

		messages := scenario.ctxStore.Messages()
		if len(messages) != 4 {
			t.Fatalf("context message count = %d, want 4", len(messages))
		}
		toolMessageText := strings.TrimSpace(textFromContentParts(messages[2].Content))
		if !strings.Contains(toolMessageText, "rejected") || !strings.Contains(toolMessageText, "blocked by policy") {
			t.Fatalf("rejected tool message = %q, want contains rejected + blocked by policy", toolMessageText)
		}

		events = append(events, drainWireMessages(scenario.wireCh)...)
		if !hasApprovalRequest(events) {
			t.Fatalf("expected approval request event, got %#v", events)
		}
	})
}

type scriptedChatProvider struct {
	streams [][]llm.ChatEvent

	mu       sync.Mutex
	requests []llm.ChatRequest
	calls    int
}

func (p *scriptedChatProvider) ModelName() string {
	return "scripted"
}

func (p *scriptedChatProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedChatProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
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

type scriptedToolRegistry struct {
	definitions []llm.ToolDefinition
	executors   map[string]soul.ToolExecutor
}

func (r scriptedToolRegistry) Definitions() []llm.ToolDefinition {
	if len(r.definitions) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(r.definitions))
	copy(out, r.definitions)
	return out
}

func (r scriptedToolRegistry) Executor(name string) (soul.ToolExecutor, bool) {
	if r.executors == nil {
		return nil, false
	}
	executor, ok := r.executors[name]
	return executor, ok
}

type toolExecutorFunc func(ctx context.Context, call types.ToolCall) (types.ToolResult, error)

func (f toolExecutorFunc) Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error) {
	return f(ctx, call)
}

type approvalScenario struct {
	engine   *soul.Soul
	ctxStore *soul.SoulContext
	wireCh   chan wire.WireMessage
	executed atomic.Int32
}

func newApprovalScenario(t *testing.T) *approvalScenario {
	t.Helper()

	scenario := &approvalScenario{
		ctxStore: soul.NewSoulContext(t.TempDir()),
		wireCh:   make(chan wire.WireMessage, 32),
	}

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-approval",
						Name: "secure_write",
						Arguments: map[string]any{
							"path": "README.md",
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "approval flow completed"}},
				{Done: true},
			},
		},
	}

	registry := scriptedToolRegistry{
		definitions: []llm.ToolDefinition{{Name: "secure_write"}},
		executors: map[string]soul.ToolExecutor{
			"secure_write": toolExecutorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				scenario.executed.Add(1)
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "tool approved and executed",
					},
				}, nil
			}),
		},
	}

	scenario.engine = soul.NewSoul(
		provider,
		scenario.ctxStore,
		registry,
		wire.ChannelEmitter{Ch: scenario.wireCh},
		"",
	)
	return scenario
}

type runOutcome struct {
	result soul.StepResult
	err    error
}

func waitRunOutcome(t *testing.T, outcomeCh <-chan runOutcome, timeout time.Duration) runOutcome {
	t.Helper()
	select {
	case outcome := <-outcomeCh:
		return outcome
	case <-time.After(timeout):
		t.Fatal("timeout waiting run outcome")
		return runOutcome{}
	}
}

func waitForApprovalRequest(t *testing.T, ch <-chan wire.WireMessage, timeout time.Duration) (wire.ApprovalRequest, []wire.WireMessage) {
	t.Helper()

	events := make([]wire.WireMessage, 0, 8)
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if msg == nil {
				t.Fatal("received nil wire message while waiting approval request")
			}
			events = append(events, msg)
			if request, ok := msg.(wire.ApprovalRequest); ok {
				return request, events
			}
		case <-deadline:
			t.Fatal("timeout waiting approval request wire event")
			return wire.ApprovalRequest{}, nil
		}
	}
}

func hasApprovalRequest(events []wire.WireMessage) bool {
	for i := range events {
		if _, ok := events[i].(wire.ApprovalRequest); ok {
			return true
		}
	}
	return false
}

func textFromContentParts(parts types.ContentParts) string {
	var sb strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			sb.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				sb.WriteString(typed.Text)
			}
		}
	}
	return sb.String()
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
