package wire

import (
	"strings"
	"testing"
)

var (
	_ Emitter = ChannelEmitter{}
	_ Emitter = NoopEmitter{}
)

func TestChannelEmitterEmit(t *testing.T) {
	t.Parallel()

	ch := make(chan WireMessage, 1)
	emitter := ChannelEmitter{Ch: ch}
	msg := TurnBegin{TurnID: "turn-1"}

	if err := emitter.Emit(msg); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	select {
	case got := <-ch:
		assertSameMessage(t, got, msg)
	default:
		t.Fatal("channel should receive one message")
	}
}

func TestChannelEmitterEmitErrors(t *testing.T) {
	t.Parallel()

	if err := (ChannelEmitter{}).Emit(TurnBegin{TurnID: "turn-1"}); err == nil || !strings.Contains(err.Error(), "nil channel") {
		t.Fatalf("Emit() with nil channel error = %v", err)
	}

	ch := make(chan WireMessage, 1)
	if err := (ChannelEmitter{Ch: ch}).Emit(nil); err == nil || !strings.Contains(err.Error(), "nil message") {
		t.Fatalf("Emit() with nil message error = %v", err)
	}
}

func TestNoopEmitterEmit(t *testing.T) {
	t.Parallel()

	if err := (NoopEmitter{}).Emit(TurnBegin{TurnID: "turn-1"}); err != nil {
		t.Fatalf("Emit() unexpected error = %v", err)
	}
	if err := (NoopEmitter{}).Emit(nil); err != nil {
		t.Fatalf("Emit(nil) unexpected error = %v", err)
	}
}
