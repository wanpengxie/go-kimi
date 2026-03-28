package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const defaultScriptedEchoModel = "scripted-echo"

var errScriptedResponseExhausted = errors.New("scripted echo: no scripted response available")

type scriptedResponses struct {
	mu        sync.Mutex
	responses []ChatResponse
	next      int
}

// ScriptedEchoChatProvider returns predefined responses one by one.
type ScriptedEchoChatProvider struct {
	model          string
	thinkingEffort ThinkingEffort
	script         *scriptedResponses
}

var _ ChatProvider = (*ScriptedEchoChatProvider)(nil)
var _ ThinkingProvider = (*ScriptedEchoChatProvider)(nil)

// NewScriptedEchoChatProvider creates one scripted provider using response sequence.
func NewScriptedEchoChatProvider(model string, script []ChatResponse) *ScriptedEchoChatProvider {
	cloned := make([]ChatResponse, len(script))
	for i := range script {
		cloned[i] = cloneChatResponse(script[i])
	}

	return &ScriptedEchoChatProvider{
		model:          normalizeEchoModel(model, defaultScriptedEchoModel),
		thinkingEffort: ThinkingOff,
		script: &scriptedResponses{
			responses: cloned,
		},
	}
}

// ModelName returns the configured model identifier.
func (p *ScriptedEchoChatProvider) ModelName() string {
	if p == nil {
		return ""
	}
	return p.model
}

// WithModel clones the provider while preserving scripted state.
func (p *ScriptedEchoChatProvider) WithModel(model string) ChatProvider {
	if p == nil {
		return p
	}
	cloned := *p
	cloned.model = normalizeEchoModel(model, p.model)
	return &cloned
}

// WithThinking clones the provider while preserving scripted state.
func (p *ScriptedEchoChatProvider) WithThinking(effort ThinkingEffort) ChatProvider {
	if p == nil {
		return p
	}
	cloned := *p
	cloned.thinkingEffort = NormalizeThinkingEffort(effort)
	return &cloned
}

// Chat returns the next scripted response.
func (p *ScriptedEchoChatProvider) Chat(ctx context.Context, _ ChatRequest) (*ChatResponse, error) {
	if p == nil {
		return nil, errors.New("scripted echo: nil provider")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.script == nil {
		return nil, errScriptedResponseExhausted
	}

	p.script.mu.Lock()
	defer p.script.mu.Unlock()
	if p.script.next >= len(p.script.responses) {
		return nil, fmt.Errorf("%w (consumed=%d)", errScriptedResponseExhausted, p.script.next)
	}

	resp := cloneChatResponse(p.script.responses[p.script.next])
	p.script.next++
	return &resp, nil
}

// ChatStream emits one stream sequence based on the next scripted response.
func (p *ScriptedEchoChatProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan ChatEvent)
	go func() {
		defer close(ch)

		for i := range resp.Content {
			if !emitChatEvent(ctx, ch, ChatEvent{Delta: resp.Content[i]}) {
				return
			}
		}
		for i := range resp.ToolCalls {
			call := resp.ToolCalls[i]
			if !emitChatEvent(ctx, ch, ChatEvent{ToolCall: &call}) {
				return
			}
		}

		done := ChatEvent{Done: true}
		if resp.Usage != (types.TokenUsage{}) {
			usage := resp.Usage
			done.Usage = &usage
		}
		_ = emitChatEvent(ctx, ch, done)
	}()

	return ch, nil
}

func cloneChatResponse(in ChatResponse) ChatResponse {
	out := in
	if len(in.Content) > 0 {
		out.Content = append(types.ContentParts(nil), in.Content...)
	}
	if len(in.ToolCalls) > 0 {
		out.ToolCalls = append([]types.ToolCall(nil), in.ToolCalls...)
	}
	return out
}
