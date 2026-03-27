package subagents

import (
	"context"
	"sync"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

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

type mockToolRegistry struct {
	definitions []llm.ToolDefinition
	executors   map[string]toolExecutorFunc
}

func (r mockToolRegistry) Definitions() []llm.ToolDefinition {
	if len(r.definitions) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(r.definitions))
	copy(out, r.definitions)
	return out
}

func (r mockToolRegistry) Executor(name string) (soul.ToolExecutor, bool) {
	if r.executors == nil {
		return nil, false
	}
	executor, ok := r.executors[name]
	if !ok {
		return nil, false
	}
	return executor, true
}

type toolExecutorFunc func(ctx context.Context, call types.ToolCall) (types.ToolResult, error)

func (f toolExecutorFunc) Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error) {
	return f(ctx, call)
}
