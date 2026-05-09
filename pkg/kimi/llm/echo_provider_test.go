package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestEchoChatProviderChat(t *testing.T) {
	t.Parallel()

	provider := NewEchoChatProvider("echo-v1")
	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{
				Role: "system",
				Content: types.ContentParts{
					types.TextPart{Text: "system"},
				},
			},
			{
				Role: "user",
				Content: types.ContentParts{
					types.TextPart{Text: "hello echo"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("StopReason = %q, want stop", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(resp.Content))
	}
	part, ok := resp.Content[0].(types.TextPart)
	if !ok {
		t.Fatalf("content[0] type = %T, want types.TextPart", resp.Content[0])
	}
	if part.Text != "hello echo" {
		t.Fatalf("content text = %q, want %q", part.Text, "hello echo")
	}
}

func TestEchoChatProviderChatStream(t *testing.T) {
	t.Parallel()

	provider := NewEchoChatProvider("echo-v1")
	stream, err := provider.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{
			{
				Role: "user",
				Content: types.ContentParts{
					types.TextPart{Text: "abc"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	var textBuilder strings.Builder
	eventCount := 0
	done := false
	for event := range stream {
		eventCount++
		if event.Delta != nil {
			text, ok := event.Delta.(types.TextPart)
			if !ok {
				t.Fatalf("delta type = %T, want types.TextPart", event.Delta)
			}
			textBuilder.WriteString(text.Text)
		}
		if event.Done {
			done = true
		}
	}

	if !done {
		t.Fatal("stream must end with Done=true event")
	}
	if eventCount != 4 {
		t.Fatalf("event count = %d, want 4 (3 rune events + done)", eventCount)
	}
	if textBuilder.String() != "abc" {
		t.Fatalf("stream text = %q, want %q", textBuilder.String(), "abc")
	}
}

func TestEchoChatProviderWithModelAndThinking(t *testing.T) {
	t.Parallel()

	base := NewEchoChatProvider("")
	if base.ModelName() != defaultEchoModel {
		t.Fatalf("default model = %q, want %q", base.ModelName(), defaultEchoModel)
	}

	overridden, ok := base.WithModel("echo-v2").(*EchoChatProvider)
	if !ok {
		t.Fatalf("WithModel() type = %T, want *EchoChatProvider", base.WithModel("echo-v2"))
	}
	if overridden == base {
		t.Fatal("WithModel() must return cloned provider")
	}
	if overridden.ModelName() != "echo-v2" {
		t.Fatalf("overridden model = %q, want %q", overridden.ModelName(), "echo-v2")
	}

	withThinking, ok := WithThinking(base, ThinkingHigh).(*EchoChatProvider)
	if !ok {
		t.Fatalf("WithThinking() type = %T, want *EchoChatProvider", WithThinking(base, ThinkingHigh))
	}
	if withThinking.thinkingEffort != ThinkingHigh {
		t.Fatalf("thinking effort = %q, want %q", withThinking.thinkingEffort, ThinkingHigh)
	}
}
