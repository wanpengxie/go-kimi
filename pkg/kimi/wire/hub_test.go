package wire

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

var (
	_ Emitter = (*Hub)(nil)
)

func TestHubSubscribePublishUnsubscribe(t *testing.T) {
	t.Parallel()

	hub := NewHub(2)
	sub := hub.Subscribe()

	msg := TurnBegin{TurnID: "turn-1"}
	hub.Publish(msg)

	select {
	case got := <-sub:
		assertSameMessage(t, got, msg)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber did not receive published message")
	}

	hub.Unsubscribe(sub)
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("subscriber channel should be closed after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("unsubscribe should close subscriber channel")
	}
}

func TestHubPublishDropsWhenSubscriberBufferIsFull(t *testing.T) {
	t.Parallel()

	hub := NewHub(1)
	sub := hub.Subscribe()

	hub.Publish(TextDelta{TurnID: "turn-1", Delta: "a"})
	hub.Publish(TextDelta{TurnID: "turn-1", Delta: "b"})

	select {
	case got := <-sub:
		delta, ok := got.(TextDelta)
		if !ok {
			t.Fatalf("message type = %T, want TextDelta", got)
		}
		if delta.Delta != "a" {
			t.Fatalf("delta = %q, want %q", delta.Delta, "a")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber did not receive first buffered message")
	}
}

func TestHub_ConcurrentPublishUnsubscribe(t *testing.T) {
	t.Parallel()

	hub := NewHub(1)
	defer hub.Close()

	stableSub := hub.Subscribe()
	done := make(chan struct{})
	panicCh := make(chan any, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			case _, ok := <-stableSub:
				if !ok {
					return
				}
			}
		}
	}()

	startPublisher := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					select {
					case panicCh <- recovered:
					default:
					}
				}
			}()

			msg := TextDelta{TurnID: "turn-1", Delta: "delta"}
			for {
				select {
				case <-done:
					return
				default:
					hub.Publish(msg)
				}
			}
		}()
	}

	startMutator := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					sub := hub.Subscribe()
					hub.Unsubscribe(sub)
				}
			}
		}()
	}

	for i := 0; i < 4; i++ {
		startPublisher()
		startMutator()
	}

	time.Sleep(500 * time.Millisecond)
	close(done)
	wg.Wait()

	select {
	case recovered := <-panicCh:
		t.Fatalf("Publish panicked under concurrent unsubscribe/close: %v", recovered)
	default:
	}
}

func TestMergingSubscriberMergesTurnTextDeltasIntoTurnEnd(t *testing.T) {
	t.Parallel()

	hub := NewHub(8)
	merger := NewMergingSubscriber(hub, 8)
	defer merger.Close()

	hub.Publish(TurnBegin{TurnID: "turn-1"})
	hub.Publish(TextDelta{TurnID: "turn-1", Delta: "hello"})
	hub.Publish(TextDelta{TurnID: "turn-1", Delta: " world"})
	hub.Publish(TurnEnd{TurnID: "turn-1"})

	first := mustReadMergedMessage(t, merger.Messages())
	if _, ok := first.(TurnBegin); !ok {
		t.Fatalf("first message type = %T, want TurnBegin", first)
	}

	second := mustReadMergedMessage(t, merger.Messages())
	turnEnd, ok := second.(TurnEnd)
	if !ok {
		t.Fatalf("second message type = %T, want TurnEnd", second)
	}
	if len(turnEnd.Output) == 0 {
		t.Fatal("turn_end output is empty, want merged text content")
	}
	text, ok := turnEnd.Output[0].(types.TextPart)
	if !ok {
		t.Fatalf("turn_end output[0] type = %T, want TextPart", turnEnd.Output[0])
	}
	if strings.TrimSpace(text.Text) != "hello world" {
		t.Fatalf("merged text = %q, want %q", text.Text, "hello world")
	}
}

func TestRecorderPersistsMessages(t *testing.T) {
	t.Parallel()

	wirePath := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := NewWireFile(wirePath)

	source := make(chan WireMessage, 4)
	recorder := NewRecorder(wireFile, source)

	source <- TurnBegin{TurnID: "turn-1"}
	source <- TurnEnd{
		TurnID: "turn-1",
		Output: types.ContentParts{
			types.TextPart{Text: "done"},
		},
	}
	close(source)

	if err := recorder.Close(); err != nil {
		t.Fatalf("Recorder.Close() error = %v", err)
	}

	iter, err := wireFile.IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() error = %v", err)
	}
	defer func() {
		if closeErr := iter.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()

	count := 0
	for iter.Next() {
		count++
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iter.Err() = %v", err)
	}
	if count != 2 {
		t.Fatalf("record count = %d, want 2", count)
	}
}

func TestRecorderCloseReturnsWhenSourceStaysOpen(t *testing.T) {
	t.Parallel()

	wirePath := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := NewWireFile(wirePath)
	source := make(chan WireMessage)
	recorder := NewRecorder(wireFile, source)

	done := make(chan error, 1)
	go func() {
		done <- recorder.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Recorder.Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Recorder.Close() blocked while source channel stayed open")
	}

	close(source)
}

func TestRecorderCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	wirePath := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := NewWireFile(wirePath)
	source := make(chan WireMessage)
	recorder := NewRecorder(wireFile, source)

	for i := 0; i < 2; i++ {
		done := make(chan error, 1)
		go func() {
			done <- recorder.Close()
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Recorder.Close() #%d error = %v", i+1, err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("Recorder.Close() #%d blocked", i+1)
		}
	}

	close(source)
}

func mustReadMergedMessage(t *testing.T, messages <-chan WireMessage) WireMessage {
	t.Helper()
	select {
	case msg, ok := <-messages:
		if !ok {
			t.Fatal("merged channel closed unexpectedly")
		}
		return msg
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting merged message")
		return nil
	}
}

// TestMergeTurnOutputCollapsesChunkedOutputAlways verifies that
// mergeTurnOutput collapses adjacent TextPart entries on TurnEnd.Output
// regardless of whether the provider streamed any text via TextDelta. Some
// providers (DeepSeek anthropic-compat) double-fill Output with N TextPart
// entries while also emitting TextDelta; the previous bypass-when-text-
// present check left those chunks in place and fed the self-perpetuating
// chunked-content bug.
func TestMergeTurnOutputCollapsesChunkedOutputAlways(t *testing.T) {
	t.Parallel()

	chunked := make(types.ContentParts, 0, 64)
	for i := 0; i < 64; i++ {
		chunked = append(chunked, types.TextPart{Text: "x"})
	}

	merged := mergeTurnOutput(chunked, "")
	if len(merged) != 1 {
		t.Fatalf("merged length = %d, want 1 (got %#v)", len(merged), merged)
	}
	text, ok := merged[0].(types.TextPart)
	if !ok {
		t.Fatalf("merged[0] type = %T, want TextPart", merged[0])
	}
	if len(text.Text) != 64 || text.Text != strings.Repeat("x", 64) {
		t.Fatalf("merged[0].Text len = %d, want 64 x's", len(text.Text))
	}
}

// TestMergeTurnOutputCollapsesAroundThinkingBlocks ensures the merger
// preserves a ThinkPart inserted between chunked text runs.
func TestMergeTurnOutputCollapsesAroundThinkingBlocks(t *testing.T) {
	t.Parallel()

	output := types.ContentParts{
		types.TextPart{Text: "pre"},
		types.TextPart{Text: "amble "},
		types.ThinkPart{Think: "reason", Signature: "sig"},
		types.TextPart{Text: "post"},
		types.TextPart{Text: "amble"},
	}

	merged := mergeTurnOutput(output, "")
	if len(merged) != 3 {
		t.Fatalf("merged length = %d, want 3 (text, think, text)", len(merged))
	}
	if text, ok := merged[0].(types.TextPart); !ok || text.Text != "preamble " {
		t.Fatalf("merged[0] = %#v, want text{preamble }", merged[0])
	}
	if think, ok := merged[1].(types.ThinkPart); !ok || think.Signature != "sig" {
		t.Fatalf("merged[1] = %#v, want think{signature preserved}", merged[1])
	}
	if text, ok := merged[2].(types.TextPart); !ok || text.Text != "postamble" {
		t.Fatalf("merged[2] = %#v, want text{postamble}", merged[2])
	}
}

// TestMergeTurnOutputFallsBackToStreamMergedText keeps the original
// behaviour: if the provider emitted no text into Output but streamed
// tokens via TextDelta, the stream-merged accumulator is appended.
func TestMergeTurnOutputFallsBackToStreamMergedText(t *testing.T) {
	t.Parallel()

	output := types.ContentParts{
		types.ThinkPart{Think: "reasoning"},
	}

	merged := mergeTurnOutput(output, " hello world ")
	if len(merged) != 2 {
		t.Fatalf("merged length = %d, want 2 (think, text)", len(merged))
	}
	if _, ok := merged[0].(types.ThinkPart); !ok {
		t.Fatalf("merged[0] = %#v, want ThinkPart", merged[0])
	}
	if text, ok := merged[1].(types.TextPart); !ok || text.Text != "hello world" {
		t.Fatalf("merged[1] = %#v, want trimmed TextPart", merged[1])
	}
}
