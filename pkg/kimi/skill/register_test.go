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

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
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
}

func (*scriptedProvider) ModelName() string {
	return "scripted"
}

func (p *scriptedProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (*scriptedProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("scripted provider: Chat not implemented")
}

func (p *scriptedProvider) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests = append(p.requests, cloneChatRequest(req))

	if p.calls >= len(p.streams) {
		return nil, errors.New("scripted provider: no stream configured for call")
	}

	streamEvents := p.streams[p.calls]
	p.calls++

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
