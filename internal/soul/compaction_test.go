package soul

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestSimpleCompactionPreservesRecentRounds(t *testing.T) {
	t.Parallel()

	history := []Message{
		{Role: RoleUser, Content: types.ContentParts{types.TextPart{Text: "u1"}}},
		{Role: RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a1"}}},
		{Role: RoleUser, Content: types.ContentParts{types.TextPart{Text: "u2"}}},
		{Role: RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a2"}}},
		{Role: RoleUser, Content: types.ContentParts{types.TextPart{Text: "u3"}}},
		{Role: RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a3"}}},
	}
	provider := &compactionProvider{
		response: &llm.ChatResponse{
			Content: types.ContentParts{
				types.TextPart{Text: "history summary"},
			},
			Usage: types.TokenUsage{
				TotalTokens: 7,
			},
		},
	}

	result, err := (&SimpleCompaction{PreserveLastN: 2, Instruction: "custom"}).Compact(context.Background(), history, provider)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("provider.Chat call count = %d, want 1", len(provider.requests))
	}
	if got := provider.requests[0].Messages[0].Role; got != "system" {
		t.Fatalf("summary request role[0] = %q, want system", got)
	}
	if got := provider.requests[0].Messages[1].Role; got != "user" {
		t.Fatalf("summary request role[1] = %q, want user", got)
	}
	if !strings.Contains(contentPartsText(provider.requests[0].Messages[1].Content), "\"u1\"") {
		t.Fatalf("summary prompt = %q, want includes first round", contentPartsText(provider.requests[0].Messages[1].Content))
	}

	if result.Usage == nil || result.Usage.TotalTokens != 7 {
		t.Fatalf("result.Usage = %#v, want total_tokens=7", result.Usage)
	}
	if len(result.Messages) != 5 {
		t.Fatalf("len(result.Messages) = %d, want 5", len(result.Messages))
	}
	if result.Messages[0].Role != RoleSystem {
		t.Fatalf("result.Messages[0].Role = %q, want system", result.Messages[0].Role)
	}
	if text := contentPartsText(result.Messages[0].Content); text != "history summary" {
		t.Fatalf("summary text = %q, want %q", text, "history summary")
	}

	gotTail := result.Messages[1:]
	wantTail := history[2:]
	if !reflect.DeepEqual(gotTail, wantTail) {
		t.Fatalf("preserved tail mismatch\ngot = %#v\nwant = %#v", gotTail, wantTail)
	}
}

func TestSimpleCompactionSkipsWhenNoOlderRounds(t *testing.T) {
	t.Parallel()

	history := []Message{
		{Role: RoleUser, Content: types.ContentParts{types.TextPart{Text: "u1"}}},
		{Role: RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a1"}}},
	}
	provider := &compactionProvider{
		response: &llm.ChatResponse{
			Content: types.ContentParts{
				types.TextPart{Text: "unused"},
			},
		},
	}

	result, err := (&SimpleCompaction{PreserveLastN: 2}).Compact(context.Background(), history, provider)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider.Chat call count = %d, want 0", len(provider.requests))
	}
	if !reflect.DeepEqual(result.Messages, history) {
		t.Fatalf("result.Messages mismatch\ngot = %#v\nwant = %#v", result.Messages, history)
	}
}

func TestShouldAutoCompact(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		tokenCount   int64
		maxContext   int
		triggerRatio float64
		reserved     int
		want         bool
	}{
		{name: "meets threshold", tokenCount: 800, maxContext: 1000, triggerRatio: 0.8, reserved: 0, want: true},
		{name: "below threshold", tokenCount: 799, maxContext: 1000, triggerRatio: 0.8, reserved: 0, want: false},
		{name: "reserved space considered", tokenCount: 640, maxContext: 1000, triggerRatio: 0.8, reserved: 200, want: true},
		{name: "invalid max context", tokenCount: 100, maxContext: 0, triggerRatio: 0.8, reserved: 0, want: false},
		{name: "invalid token count", tokenCount: 0, maxContext: 1000, triggerRatio: 0.8, reserved: 0, want: false},
		{name: "default ratio fallback", tokenCount: 8, maxContext: 10, triggerRatio: 0, reserved: 0, want: true},
		{name: "reserved exceeds context", tokenCount: 500, maxContext: 1000, triggerRatio: 0.8, reserved: 1000, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldAutoCompact(tc.tokenCount, tc.maxContext, tc.triggerRatio, tc.reserved); got != tc.want {
				t.Fatalf("shouldAutoCompact(%d,%d,%.2f,%d) = %v, want %v", tc.tokenCount, tc.maxContext, tc.triggerRatio, tc.reserved, got, tc.want)
			}
		})
	}
}

func TestSoulRunAutoCompactionAfterStep(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "assistant output"}},
				{Done: true},
			},
		},
	}

	wireCh := make(chan wire.WireMessage, 16)
	engine := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	engine.SetCompactionConfig(CompactionConfig{
		Enabled:        true,
		TriggerRatio:   0.2,
		MaxContextSize: 10,
		ReservedSize:   0,
	})

	compacted := false
	engine.SetCompactor(compactorFunc(func(_ context.Context, messages []Message, _ llm.ChatProvider) (CompactionResult, error) {
		compacted = true
		tail := cloneMessages(messages)
		if len(tail) > 2 {
			tail = tail[len(tail)-2:]
		}
		return CompactionResult{
			Messages: append([]Message{
				{
					Role:    RoleSystem,
					Content: types.ContentParts{types.TextPart{Text: "compacted summary"}},
				},
			}, tail...),
		}, nil
	}))

	if _, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: strings.Repeat("x", 64)},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !compacted {
		t.Fatal("compactor was not triggered")
	}
	messages := ctxStore.Messages()
	if len(messages) != 3 {
		t.Fatalf("context message count after compaction = %d, want 3", len(messages))
	}
	if messages[0].Role != RoleSystem {
		t.Fatalf("messages[0].Role = %q, want system", messages[0].Role)
	}
	if ctxStore.TokenCount() == 0 {
		t.Fatalf("TokenCount() = %d, want > 0", ctxStore.TokenCount())
	}

	events := drainWireMessages(wireCh)
	hasBegin := false
	hasEnd := false
	for i := range events {
		switch events[i].(type) {
		case wire.CompactionBegin:
			hasBegin = true
		case wire.CompactionEnd:
			hasEnd = true
		}
	}
	if !hasBegin || !hasEnd {
		t.Fatalf("compaction events missing: begin=%v end=%v events=%#v", hasBegin, hasEnd, events)
	}
}

func TestSoulRunCompactionSummaryFailureIsFailOpen(t *testing.T) {
	t.Parallel()

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "assistant output"}},
				{Done: true},
			},
		},
		chatErr: errors.New("summary backend unavailable"),
	}

	wireCh := make(chan wire.WireMessage, 16)
	engine := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")
	engine.SetCompactionConfig(CompactionConfig{
		Enabled:        true,
		TriggerRatio:   0.2,
		MaxContextSize: 10,
		ReservedSize:   0,
	})
	engine.SetCompactor(&SimpleCompaction{PreserveLastN: -1})

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: strings.Repeat("x", 64)},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := contentPartsText(result.Content); got != "assistant output" {
		t.Fatalf("result content = %q, want %q", got, "assistant output")
	}

	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("context message count = %d, want 2", len(messages))
	}
	if messages[0].Role != RoleUser || messages[1].Role != RoleAssistant {
		t.Fatalf("context roles = %#v, want [user assistant]", []Role{messages[0].Role, messages[1].Role})
	}

	events := drainWireMessages(wireCh)
	hasTurnEnd := false
	hasCompactionError := false
	for i := range events {
		switch typed := events[i].(type) {
		case wire.CompactionError:
			hasCompactionError = true
			if !strings.Contains(typed.Error, "summary backend unavailable") {
				t.Fatalf("CompactionError.Error = %q, want includes summary backend unavailable", typed.Error)
			}
		case wire.TurnEnd:
			hasTurnEnd = true
			if typed.StopReason != "stop" {
				t.Fatalf("TurnEnd.StopReason = %q, want stop", typed.StopReason)
			}
		}
	}
	if !hasCompactionError {
		t.Fatalf("wire events missing CompactionError: %#v", events)
	}
	if !hasTurnEnd {
		t.Fatalf("wire events missing TurnEnd: %#v", events)
	}
}

type compactionProvider struct {
	response *llm.ChatResponse
	err      error
	requests []llm.ChatRequest
}

func (p *compactionProvider) ModelName() string {
	return "compaction-provider"
}

func (p *compactionProvider) WithModel(_ string) llm.ChatProvider {
	return p
}

func (p *compactionProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *compactionProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, cloneChatRequest(req))
	if p.err != nil {
		return nil, p.err
	}
	if p.response == nil {
		return &llm.ChatResponse{}, nil
	}
	out := *p.response
	out.Content = cloneContentParts(p.response.Content)
	return &out, nil
}

func (p *compactionProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	ch := make(chan llm.ChatEvent, 1)
	ch <- llm.ChatEvent{Done: true}
	close(ch)
	return ch, nil
}

type compactorFunc func(ctx context.Context, messages []Message, provider llm.ChatProvider) (CompactionResult, error)

func (f compactorFunc) Compact(ctx context.Context, messages []Message, provider llm.ChatProvider) (CompactionResult, error) {
	return f(ctx, messages, provider)
}
