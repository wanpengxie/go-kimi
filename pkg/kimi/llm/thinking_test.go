package llm

import (
	"context"
	"testing"
)

type plainThinkingTestProvider struct{}

func (plainThinkingTestProvider) ModelName() string {
	return "plain"
}

func (plainThinkingTestProvider) WithModel(_ string) ChatProvider {
	return plainThinkingTestProvider{}
}

func (plainThinkingTestProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}

func (plainThinkingTestProvider) ChatStream(_ context.Context, _ ChatRequest) (<-chan ChatEvent, error) {
	ch := make(chan ChatEvent)
	close(ch)
	return ch, nil
}

type thinkingTestProvider struct {
	effort ThinkingEffort
}

func (p thinkingTestProvider) ModelName() string {
	return "thinking"
}

func (p thinkingTestProvider) WithModel(_ string) ChatProvider {
	return p
}

func (p thinkingTestProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}

func (p thinkingTestProvider) ChatStream(_ context.Context, _ ChatRequest) (<-chan ChatEvent, error) {
	ch := make(chan ChatEvent)
	close(ch)
	return ch, nil
}

func (p thinkingTestProvider) WithThinking(effort ThinkingEffort) ChatProvider {
	p.effort = effort
	return p
}

var _ ChatProvider = plainThinkingTestProvider{}
var _ ThinkingProvider = thinkingTestProvider{}

func TestNormalizeThinkingEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   ThinkingEffort
		want ThinkingEffort
	}{
		{name: "empty", in: "", want: ThinkingOff},
		{name: "off", in: ThinkingOff, want: ThinkingOff},
		{name: "low", in: "LOW", want: ThinkingLow},
		{name: "medium", in: " medium ", want: ThinkingMedium},
		{name: "high", in: ThinkingHigh, want: ThinkingHigh},
		{name: "invalid", in: "turbo", want: ThinkingOff},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeThinkingEffort(tc.in); got != tc.want {
				t.Fatalf("NormalizeThinkingEffort(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWithThinking(t *testing.T) {
	t.Parallel()

	t.Run("unsupported provider keeps original", func(t *testing.T) {
		t.Parallel()

		provider := plainThinkingTestProvider{}
		next := WithThinking(provider, ThinkingHigh)
		if _, ok := next.(plainThinkingTestProvider); !ok {
			t.Fatalf("WithThinking() type = %T, want plainThinkingTestProvider", next)
		}
	})

	t.Run("supported provider applies normalized effort", func(t *testing.T) {
		t.Parallel()

		provider := thinkingTestProvider{}
		next := WithThinking(provider, " HIGH ")
		typed, ok := next.(thinkingTestProvider)
		if !ok {
			t.Fatalf("WithThinking() type = %T, want thinkingTestProvider", next)
		}
		if typed.effort != ThinkingHigh {
			t.Fatalf("applied effort = %q, want %q", typed.effort, ThinkingHigh)
		}
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()

		if next := WithThinking(nil, ThinkingLow); next != nil {
			t.Fatalf("WithThinking(nil) = %T, want nil", next)
		}
	})
}
