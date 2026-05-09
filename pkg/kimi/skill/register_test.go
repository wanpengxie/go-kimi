package skill

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	skillflow "github.com/wanpengxie/go-kimi/pkg/kimi/skill/flow"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func TestRegisterSkillsRunsThroughSoulExecutionPath(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-skill",
						Name: "skill:demo",
						Arguments: map[string]any{
							"args": "extra input",
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "skill answered"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "outer done"}},
				{Done: true},
			},
		},
	}

	baseRegistry := tools.NewMapToolRegistry(&noopTool{name: "echo"})
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, baseRegistry, wire.NoopEmitter{}, "")

	RegisterSkills(engine, map[string]*Skill{
		"demo": {
			Name:        "demo",
			Description: "demo skill",
			Type:        "standard",
			Content:     "You are demo skill.",
		},
	})

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "start"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := textFromParts(result.Content); got != "outer done" {
		t.Fatalf("result text = %q, want outer done", got)
	}
	if provider.CallCount() != 3 {
		t.Fatalf("provider call count = %d, want 3", provider.CallCount())
	}

	requests := provider.Requests()
	if len(requests) != 3 {
		t.Fatalf("len(requests) = %d, want 3", len(requests))
	}

	gotToolNames := make([]string, 0, len(requests[0].Tools))
	for i := range requests[0].Tools {
		gotToolNames = append(gotToolNames, requests[0].Tools[i].Name)
	}
	sort.Strings(gotToolNames)
	if len(gotToolNames) != 2 || gotToolNames[0] != "echo" || gotToolNames[1] != "skill:demo" {
		t.Fatalf("request[0].tools = %#v, want [echo skill:demo]", gotToolNames)
	}

	nested := requests[1]
	nestedToolNames := toolNames(nested.Tools)
	if hasToolName(nested.Tools, "skill:demo") {
		t.Fatalf("nested request tools = %#v, want no skill:*", nestedToolNames)
	}
	if !hasToolName(nested.Tools, "echo") {
		t.Fatalf("nested request tools = %#v, want base tool echo", nestedToolNames)
	}
	if len(nested.Messages) == 0 {
		t.Fatalf("nested request messages = %d, want > 0", len(nested.Messages))
	}
	lastMessage := nested.Messages[len(nested.Messages)-1]
	if lastMessage.Role != "user" {
		t.Fatalf("nested last message role = %q, want user", lastMessage.Role)
	}
	lastText := textFromParts(lastMessage.Content)
	if !strings.Contains(lastText, "You are demo skill.") || !strings.Contains(lastText, "extra input") {
		t.Fatalf("nested user message = %q, want skill content + args", lastText)
	}
}

func TestRegisterSkillsCanBeCalledMultipleTimes(t *testing.T) {
	t.Parallel()

	engine := soul.NewSoul(&scriptedProvider{}, soul.NewSoulContext(t.TempDir()), tools.NewMapToolRegistry(), wire.NoopEmitter{}, "")

	RegisterSkills(engine, map[string]*Skill{
		"demo": {
			Name:        "demo",
			Description: "v1",
			Type:        "standard",
			Content:     "demo v1",
		},
	})
	RegisterSkills(engine, map[string]*Skill{
		"demo": {
			Name:        "demo",
			Description: "v2",
			Type:        "standard",
			Content:     "demo v2",
		},
		"extra": {
			Name:        "extra",
			Description: "extra",
			Type:        "standard",
			Content:     "extra",
		},
	})

	defs := engine.ToolRegistry().Definitions()
	byName := map[string]llm.ToolDefinition{}
	for i := range defs {
		byName[defs[i].Name] = defs[i]
	}

	if got := strings.TrimSpace(byName["skill:demo"].Description); got != "v2" {
		t.Fatalf("skill:demo description = %q, want v2", got)
	}
	if _, ok := byName["skill:extra"]; !ok {
		t.Fatalf("skill:extra missing in definitions = %#v", defs)
	}
}

func TestRegisterSkillsNestedSkillCallDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-outer",
						Name: "skill:demo",
					},
				},
				{Done: true},
			},
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-nested",
						Name: "skill:demo",
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "nested done"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "outer done"}},
				{Done: true},
			},
		},
	}

	baseRegistry := tools.NewMapToolRegistry(&noopTool{name: "echo"})
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, baseRegistry, wire.NoopEmitter{}, "")

	RegisterSkills(engine, map[string]*Skill{
		"demo": {
			Name:        "demo",
			Description: "demo skill",
			Type:        "standard",
			Content:     "You are demo skill.",
		},
	})

	type runResult struct {
		result soul.StepResult
		err    error
	}

	done := make(chan runResult, 1)
	go func() {
		result, err := engine.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "start"},
		})
		done <- runResult{result: result, err: err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run() error = %v", out.err)
		}
		if got := textFromParts(out.result.Content); got != "outer done" {
			t.Fatalf("result text = %q, want outer done", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() timeout, possible nested skill deadlock")
	}

	if provider.CallCount() != 4 {
		t.Fatalf("provider call count = %d, want 4", provider.CallCount())
	}

	requests := provider.Requests()
	if len(requests) != 4 {
		t.Fatalf("len(requests) = %d, want 4", len(requests))
	}

	nestedToolNames := toolNames(requests[1].Tools)
	if hasToolName(requests[1].Tools, "skill:demo") {
		t.Fatalf("nested request tools = %#v, want no skill:*", nestedToolNames)
	}
}

func TestRegisterSkillsParallelSkillToolCallsRemainAvailable(t *testing.T) {
	t.Parallel()

	firstSkillRunGate := make(chan struct{})
	provider := &scriptedProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-one",
						Name: "skill:one",
					},
				},
				{
					ToolCall: &types.ToolCall{
						ID:   "call-two",
						Name: "skill:two",
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "skill done"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "skill done"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "outer done"}},
				{Done: true},
			},
		},
		beforeStream: func(call int) {
			if call == 1 {
				<-firstSkillRunGate
			}
		},
	}

	baseRegistry := tools.NewMapToolRegistry(&noopTool{name: "echo"})
	ctxStore := soul.NewSoulContext(t.TempDir())
	wireCh := make(chan wire.WireMessage, 64)
	engine := soul.NewSoul(provider, ctxStore, baseRegistry, wire.ChannelEmitter{Ch: wireCh}, "")
	engine.SetYolo(false)

	RegisterSkills(engine, map[string]*Skill{
		"one": {
			Name:        "one",
			Description: "skill one",
			Type:        "standard",
			Content:     "You are skill one.",
		},
		"two": {
			Name:        "two",
			Description: "skill two",
			Type:        "standard",
			Content:     "You are skill two.",
		},
	})

	registry, ok := engine.ToolRegistry().(*skillRegistry)
	if !ok {
		t.Fatalf("tool registry type = %T, want *skillRegistry", engine.ToolRegistry())
	}

	type runResult struct {
		result soul.StepResult
		err    error
	}

	done := make(chan runResult, 1)
	go func() {
		result, err := engine.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "start"},
		})
		done <- runResult{result: result, err: err}
	}()

	approvals := waitApprovalRequests(t, wireCh, 2)
	if err := engine.RespondApproval(approvals[0].ID, soul.ApprovalApprove, ""); err != nil {
		t.Fatalf("RespondApproval(first) error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for registry.nestedRuns.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nestedRuns did not become > 0 before second approval")
		}
		time.Sleep(time.Millisecond)
	}

	if err := engine.RespondApproval(approvals[1].ID, soul.ApprovalApprove, ""); err != nil {
		t.Fatalf("RespondApproval(second) error = %v", err)
	}
	close(firstSkillRunGate)

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run() error = %v", out.err)
		}
		if got := textFromParts(out.result.Content); got != "outer done" {
			t.Fatalf("result text = %q, want outer done", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() timeout, possible parallel skill call regression")
	}

	if provider.CallCount() != 4 {
		t.Fatalf("provider call count = %d, want 4", provider.CallCount())
	}

	requests := provider.Requests()
	if len(requests) != 4 {
		t.Fatalf("len(requests) = %d, want 4", len(requests))
	}

	outerFollowup := requests[3]
	toolPayloads := make([]string, 0, 2)
	for i := range outerFollowup.Messages {
		if outerFollowup.Messages[i].Role != "tool" {
			continue
		}
		toolPayloads = append(toolPayloads, textFromParts(outerFollowup.Messages[i].Content))
	}
	if len(toolPayloads) != 2 {
		t.Fatalf("outer followup tool payload count = %d, want 2", len(toolPayloads))
	}
	for i := range toolPayloads {
		if toolPayloads[i] != "skill done" {
			t.Fatalf("tool payload[%d] = %q, want skill done", i, toolPayloads[i])
		}
	}
}

func TestRegisterSkillsFlowSkillRunsThroughFlowRunner(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-flow",
						Name: "skill:review",
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "analyzed"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "pick <choice>done</choice>"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "flow finished"}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "outer done"}},
				{Done: true},
			},
		},
	}

	engine := soul.NewSoul(provider, soul.NewSoulContext(t.TempDir()), tools.NewMapToolRegistry(&noopTool{name: "echo"}), wire.NoopEmitter{}, "")

	flowGraph := &skillflow.Flow{
		Nodes: map[string]skillflow.FlowNode{
			"BEGIN": {ID: "BEGIN", Label: "BEGIN", Kind: skillflow.NodeKindBegin},
			"A":     {ID: "A", Label: "analyze code", Kind: skillflow.NodeKindTask},
			"B":     {ID: "B", Label: "is it done?", Kind: skillflow.NodeKindDecision},
			"C":     {ID: "C", Label: "write summary", Kind: skillflow.NodeKindTask},
			"END":   {ID: "END", Label: "END", Kind: skillflow.NodeKindEnd},
		},
		Outgoing: map[string][]skillflow.FlowEdge{
			"BEGIN": {{Src: "BEGIN", Dst: "A"}},
			"A":     {{Src: "A", Dst: "B"}},
			"B": {
				{Src: "B", Dst: "A", Label: "retry"},
				{Src: "B", Dst: "C", Label: "done"},
			},
			"C":   {{Src: "C", Dst: "END"}},
			"END": nil,
		},
		BeginID: "BEGIN",
		EndID:   "END",
	}

	RegisterSkills(engine, map[string]*Skill{
		"review": {
			Name:        "review",
			Description: "flow review",
			Type:        "flow",
			Flow:        flowGraph,
		},
	})

	result, err := engine.Run(context.Background(), types.ContentParts{types.TextPart{Text: "start"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := textFromParts(result.Content); got != "outer done" {
		t.Fatalf("result text = %q, want outer done", got)
	}
	if provider.CallCount() != 5 {
		t.Fatalf("provider call count = %d, want 5", provider.CallCount())
	}

	requests := provider.Requests()
	if len(requests) != 5 {
		t.Fatalf("len(requests) = %d, want 5", len(requests))
	}
	decisionPrompt := textFromParts(requests[2].Messages[len(requests[2].Messages)-1].Content)
	if !strings.Contains(decisionPrompt, "Available branches:") {
		t.Fatalf("decision prompt = %q, want branch hints", decisionPrompt)
	}

	outerFollowup := requests[4]
	toolPayload := ""
	for i := range outerFollowup.Messages {
		if outerFollowup.Messages[i].Role != "tool" {
			continue
		}
		toolPayload = textFromParts(outerFollowup.Messages[i].Content)
	}
	if toolPayload != "flow finished" {
		t.Fatalf("flow tool payload = %q, want flow finished", toolPayload)
	}
}

func waitApprovalRequests(t *testing.T, wireCh <-chan wire.WireMessage, want int) []wire.ApprovalRequest {
	t.Helper()
	approvals := make([]wire.ApprovalRequest, 0, want)
	deadline := time.After(2 * time.Second)
	for len(approvals) < want {
		select {
		case msg := <-wireCh:
			req, ok := msg.(wire.ApprovalRequest)
			if !ok {
				continue
			}
			approvals = append(approvals, req)
		case <-deadline:
			t.Fatalf("approval request count = %d, want %d", len(approvals), want)
		}
	}
	return approvals
}

type noopTool struct {
	name string
}

func (t *noopTool) Name() string {
	return t.name
}

func (*noopTool) Description() string {
	return "noop"
}

func (*noopTool) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

func (t *noopTool) Execute(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{
		Name: t.name,
		Value: types.ToolReturnValue{
			Value: "noop",
		},
	}, nil
}

type scriptedProvider struct {
	mu       sync.Mutex
	streams  [][]llm.ChatEvent
	requests []llm.ChatRequest
	calls    int
	// beforeStream runs right before emitting the configured stream for the given call index.
	beforeStream func(call int)
}

func (*scriptedProvider) ModelName() string {
	return "scripted"
}

func (p *scriptedProvider) WithModel(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (*scriptedProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("scripted provider: Chat not implemented")
}

func (p *scriptedProvider) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneChatRequest(req))

	if p.calls >= len(p.streams) {
		p.mu.Unlock()
		return nil, errors.New("scripted provider: no stream configured for call")
	}

	streamEvents := p.streams[p.calls]
	call := p.calls
	beforeStream := p.beforeStream
	p.calls++
	p.mu.Unlock()

	if beforeStream != nil {
		beforeStream(call)
	}

	ch := make(chan llm.ChatEvent, len(streamEvents))
	for i := range streamEvents {
		ch <- streamEvents[i]
	}
	close(ch)
	return ch, nil
}

func (p *scriptedProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *scriptedProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]llm.ChatRequest, len(p.requests))
	for i := range p.requests {
		out[i] = cloneChatRequest(p.requests[i])
	}
	return out
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

func toolNames(defs []llm.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for i := range defs {
		name := strings.TrimSpace(defs[i].Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func hasToolName(defs []llm.ToolDefinition, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for i := range defs {
		if strings.TrimSpace(defs[i].Name) == expected {
			return true
		}
	}
	return false
}

func textFromParts(parts types.ContentParts) string {
	var builder strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			builder.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				builder.WriteString(typed.Text)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}
