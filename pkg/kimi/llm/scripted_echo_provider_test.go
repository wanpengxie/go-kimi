package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestScriptedEchoChatProviderChatSequence(t *testing.T) {
	t.Parallel()

	provider := NewScriptedEchoChatProvider("", []ChatResponse{
		{
			Content: types.ContentParts{
				types.TextPart{Text: "first"},
			},
			StopReason: "stop",
		},
		{
			Content: types.ContentParts{
				types.TextPart{Text: "second"},
			},
			ToolCalls: []types.ToolCall{
				{
					ID:   "call-1",
					Name: "search",
					Arguments: map[string]any{
						"q": "moonshot",
					},
				},
			},
			Usage: types.TokenUsage{
				TotalTokens: 10,
			},
			StopReason: "tool_calls",
		},
	})

	if provider.ModelName() != defaultScriptedEchoModel {
		t.Fatalf("default model = %q, want %q", provider.ModelName(), defaultScriptedEchoModel)
	}

	resp1, err := provider.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() first call error = %v", err)
	}
	part1, ok := resp1.Content[0].(types.TextPart)
	if !ok || part1.Text != "first" {
		t.Fatalf("first response content = %#v, want text first", resp1.Content)
	}

	resp2, err := provider.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() second call error = %v", err)
	}
	if resp2.StopReason != "tool_calls" {
		t.Fatalf("second StopReason = %q, want tool_calls", resp2.StopReason)
	}
	if len(resp2.ToolCalls) != 1 || resp2.ToolCalls[0].Name != "search" {
		t.Fatalf("second tool calls = %#v, want one search call", resp2.ToolCalls)
	}

	_, err = provider.Chat(context.Background(), ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), errScriptedResponseExhausted.Error()) {
		t.Fatalf("third Chat() error = %v, want scripted exhaustion", err)
	}
}

func TestScriptedEchoChatProviderChatStream(t *testing.T) {
	t.Parallel()

	provider := NewScriptedEchoChatProvider("scripted-v1", []ChatResponse{
		{
			Content: types.ContentParts{
				types.TextPart{Text: "hello"},
				types.TextPart{Text: " world"},
			},
			ToolCalls: []types.ToolCall{
				{
					ID:   "call-1",
					Name: "echo",
					Arguments: map[string]any{
						"input": "x",
					},
				},
			},
			Usage: types.TokenUsage{
				InputTokens:  2,
				OutputTokens: 3,
				TotalTokens:  5,
			},
		},
	})

	stream, err := provider.ChatStream(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	events := make([]ChatEvent, 0, 4)
	for event := range stream {
		events = append(events, event)
	}

	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4", len(events))
	}
	text1, ok := events[0].Delta.(types.TextPart)
	if !ok || text1.Text != "hello" {
		t.Fatalf("event[0].Delta = %#v, want text hello", events[0].Delta)
	}
	text2, ok := events[1].Delta.(types.TextPart)
	if !ok || text2.Text != " world" {
		t.Fatalf("event[1].Delta = %#v, want text ' world'", events[1].Delta)
	}
	if events[2].ToolCall == nil || events[2].ToolCall.Name != "echo" {
		t.Fatalf("event[2].ToolCall = %#v, want echo tool call", events[2].ToolCall)
	}
	if !events[3].Done {
		t.Fatal("event[3] must be done")
	}
	if events[3].Usage == nil || events[3].Usage.TotalTokens != 5 {
		t.Fatalf("event[3].Usage = %#v, want total_tokens=5", events[3].Usage)
	}
}

func TestScriptedEchoWithModelSharesSequence(t *testing.T) {
	t.Parallel()

	base := NewScriptedEchoChatProvider("scripted-v1", []ChatResponse{
		{
			Content: types.ContentParts{
				types.TextPart{Text: "only once"},
			},
		},
	})
	overridden, ok := base.WithModel("scripted-v2").(*ScriptedEchoChatProvider)
	if !ok {
		t.Fatalf("WithModel() type = %T, want *ScriptedEchoChatProvider", base.WithModel("scripted-v2"))
	}
	if overridden.ModelName() != "scripted-v2" {
		t.Fatalf("override model = %q, want scripted-v2", overridden.ModelName())
	}

	if _, err := overridden.Chat(context.Background(), ChatRequest{}); err != nil {
		t.Fatalf("override Chat() error = %v", err)
	}
	if _, err := base.Chat(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("base Chat() should be exhausted after override consumed shared script")
	}
}
