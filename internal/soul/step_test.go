package soul

import (
	"context"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

// TestAppendContentPartCollapsingMergesChunkedDeltas — when the streaming
// loop appends one TextDelta per token, the result.Content slice MUST NOT
// grow into N TextPart entries. Each new text delta is concatenated onto
// the most recent trailing TextPart so context.jsonl receives a single
// merged TextPart per turn. (See step.go for the bug history.)
func TestAppendContentPartCollapsingMergesChunkedDeltas(t *testing.T) {
	t.Parallel()

	chunks := []string{"He", "llo", " ", "wor", "ld", "!"}
	var content types.ContentParts
	for _, chunk := range chunks {
		content = appendContentPartCollapsing(content, types.TextPart{Text: chunk})
	}

	if len(content) != 1 {
		t.Fatalf("content length = %d, want 1; got %#v", len(content), content)
	}
	text, ok := content[0].(types.TextPart)
	if !ok {
		t.Fatalf("content[0] type = %T, want TextPart", content[0])
	}
	if text.Text != "Hello world!" {
		t.Fatalf("merged text = %q, want %q", text.Text, "Hello world!")
	}
}

// TestAppendContentPartCollapsingPreservesThinkBoundary — a ThinkPart
// breaks the run; subsequent text starts a fresh TextPart. Think.Signature
// is preserved verbatim.
func TestAppendContentPartCollapsingPreservesThinkBoundary(t *testing.T) {
	t.Parallel()

	var content types.ContentParts
	content = appendContentPartCollapsing(content, types.TextPart{Text: "pre"})
	content = appendContentPartCollapsing(content, types.TextPart{Text: "amble "})
	content = appendContentPartCollapsing(content, types.ThinkPart{Think: "reason", Signature: "sig-1"})
	content = appendContentPartCollapsing(content, types.TextPart{Text: "post"})
	content = appendContentPartCollapsing(content, types.TextPart{Text: "amble"})

	if len(content) != 3 {
		t.Fatalf("content length = %d, want 3 (text, think, text); got %#v", len(content), content)
	}
	if text, ok := content[0].(types.TextPart); !ok || text.Text != "preamble " {
		t.Fatalf("content[0] = %#v, want text{preamble }", content[0])
	}
	if think, ok := content[1].(types.ThinkPart); !ok || think.Think != "reason" || think.Signature != "sig-1" {
		t.Fatalf("content[1] = %#v, want think{reason, sig-1}", content[1])
	}
	if text, ok := content[2].(types.TextPart); !ok || text.Text != "postamble" {
		t.Fatalf("content[2] = %#v, want text{postamble}", content[2])
	}
}

// TestAppendContentPartCollapsingDropsEmptyTextDeltas — empty TextPart
// (Text=="") must not produce empty entries.
func TestAppendContentPartCollapsingDropsEmptyTextDeltas(t *testing.T) {
	t.Parallel()

	var content types.ContentParts
	content = appendContentPartCollapsing(content, types.TextPart{Text: ""})
	if len(content) != 0 {
		t.Fatalf("after empty-only deltas, content length = %d, want 0", len(content))
	}

	content = appendContentPartCollapsing(content, types.TextPart{Text: "real"})
	content = appendContentPartCollapsing(content, types.TextPart{Text: ""})
	content = appendContentPartCollapsing(content, types.TextPart{Text: " text"})

	if len(content) != 1 {
		t.Fatalf("content length = %d, want 1; got %#v", len(content), content)
	}
	if text, _ := content[0].(types.TextPart); text.Text != "real text" {
		t.Fatalf("content[0] text = %q, want %q", text.Text, "real text")
	}
}

// TestAppendContentPartCollapsingHandlesLargeChunkStream simulates the
// reported failure mode: 343 TextDelta events in one turn. Without collapse
// the assistant message ends up with 343 TextPart entries, which the
// anthropic encoder explodes into 343 content_block entries; with collapse
// the entire turn is a single TextPart whose total text length equals
// sum-of-chunks.
func TestAppendContentPartCollapsingHandlesLargeChunkStream(t *testing.T) {
	t.Parallel()

	const tokenCount = 343
	const tokenText = "tok"

	var content types.ContentParts
	for i := 0; i < tokenCount; i++ {
		content = appendContentPartCollapsing(content, types.TextPart{Text: tokenText})
	}

	if len(content) != 1 {
		t.Fatalf("content length = %d, want 1 after %d chunks", len(content), tokenCount)
	}
	text, ok := content[0].(types.TextPart)
	if !ok {
		t.Fatalf("content[0] type = %T, want TextPart", content[0])
	}
	wantLen := tokenCount * len(tokenText)
	if len(text.Text) != wantLen {
		t.Fatalf("merged text length = %d, want %d", len(text.Text), wantLen)
	}
	if text.Text != strings.Repeat(tokenText, tokenCount) {
		t.Fatalf("merged text body mismatch (first 20 chars = %q)", text.Text[:20])
	}
}

// TestSoulRunMultiTurnChunkedStreamCollapsesEverywhere is the end-to-end
// verification for the chunked-content self-perpetuation fix. It runs the
// full Soul.Run loop for THREE turns against a scripted provider that
// emits 35 TextDelta events per turn (mimicking DeepSeek's
// anthropic-compat per-token streaming) plus pre-baked thinking blocks
// embedded in the stream to verify thinking blocks remain separate parts.
//
// Assertions after the third turn:
//
//  1. SoulContext (the on-disk truth) has one assistant message per turn,
//     each containing exactly 1 collapsed TextPart + the thinking parts —
//     never 35+ chunks per row.
//  2. The provider request for turn N contains the prior turns' assistant
//     messages with collapsed Content (normalizeHistory's within-message
//     defense kicks in even if upstream layers ever regress).
//  3. No self-perpetuation: across 3 turns the assistant text length stays
//     constant (35 chunks of 2 chars = 70 chars), it does not grow as the
//     model would if it observed and mimicked the chunked format.
func TestSoulRunMultiTurnChunkedStreamCollapsesEverywhere(t *testing.T) {
	t.Parallel()

	const chunksPerTurn = 35
	const chunkText = "ab"
	const numTurns = 3

	// Build per-turn event slices: TextDelta × 35 + a single ThinkPart in
	// the middle + Done. Same shape for every turn so we can compare
	// stability across turns.
	streams := make([][]llm.ChatEvent, 0, numTurns)
	for turn := 0; turn < numTurns; turn++ {
		events := make([]llm.ChatEvent, 0, chunksPerTurn+3)
		for i := 0; i < chunksPerTurn/2; i++ {
			events = append(events, llm.ChatEvent{Delta: types.TextPart{Text: chunkText}})
		}
		events = append(events, llm.ChatEvent{Delta: types.ThinkPart{
			Think:     "mid-stream reasoning",
			Signature: "sig-turn-" + string(rune('A'+turn)),
		}})
		for i := chunksPerTurn / 2; i < chunksPerTurn; i++ {
			events = append(events, llm.ChatEvent{Delta: types.TextPart{Text: chunkText}})
		}
		events = append(events, llm.ChatEvent{
			Usage: &types.TokenUsage{InputTokens: 1, OutputTokens: chunksPerTurn, TotalTokens: 1 + chunksPerTurn},
			Done:  true,
		})
		streams = append(streams, events)
	}

	ctxStore := NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{streams: streams}
	wireCh := make(chan wire.WireMessage, 256)
	s := NewSoul(provider, ctxStore, mockRegistry{}, wire.ChannelEmitter{Ch: wireCh}, "")

	// Drive 3 turns. Each Run call sends one user message and runs the
	// step loop until the model stops emitting tool calls (immediately,
	// here — no tools).
	for turn := 0; turn < numTurns; turn++ {
		userInput := types.ContentParts{types.TextPart{Text: "turn user input"}}
		if _, err := s.Run(context.Background(), userInput); err != nil {
			t.Fatalf("Run turn %d error = %v", turn+1, err)
		}
	}

	// Assertion 1: SoulContext rows are collapsed.
	messages := ctxStore.Messages()
	if len(messages) != numTurns*2 {
		t.Fatalf("context message count = %d, want %d (user+assistant × %d turns)",
			len(messages), numTurns*2, numTurns)
	}

	// Stream layout per turn: 17 text deltas, 1 think delta, 18 text
	// deltas, 1 done. After collapse the assistant message should be
	// exactly [TextPart(34 chars), ThinkPart, TextPart(36 chars)] — i.e.
	// 3 content parts total, 2 text parts (split by the ThinkPart
	// boundary, not 35 unchunked text parts). Total text length =
	// chunksPerTurn * len(chunkText), constant across all 3 turns.
	wantTextLen := chunksPerTurn * len(chunkText)
	for i, msg := range messages {
		if i%2 == 0 {
			// user message; skip
			continue
		}
		turn := (i + 1) / 2
		if got := len(msg.Content); got != 3 {
			t.Fatalf("turn %d assistant: content part count = %d, want 3 (text, think, text); got %#v",
				turn, got, msg.Content)
		}
		textParts := 0
		thinkParts := 0
		var concatText string
		for _, part := range msg.Content {
			switch typed := part.(type) {
			case types.TextPart:
				textParts++
				concatText += typed.Text
			case types.ThinkPart:
				thinkParts++
				if typed.Signature == "" {
					t.Fatalf("turn %d assistant: thinking signature lost", turn)
				}
			default:
				t.Fatalf("turn %d assistant: unexpected content part %T", turn, part)
			}
		}
		if textParts != 2 {
			t.Fatalf("turn %d assistant: text part count = %d, want 2 (split by think boundary, not %d unchunked)",
				turn, textParts, chunksPerTurn)
		}
		if thinkParts != 1 {
			t.Fatalf("turn %d assistant: thinking part count = %d, want 1", turn, thinkParts)
		}
		if len(concatText) != wantTextLen {
			t.Fatalf("turn %d assistant: text length = %d, want %d (chunks should not snowball)",
				turn, len(concatText), wantTextLen)
		}
		if concatText != strings.Repeat(chunkText, chunksPerTurn) {
			t.Fatalf("turn %d assistant: text body mismatch", turn)
		}
	}

	// Assertion 2: provider requests show collapsed history.
	requests := provider.Requests()
	if len(requests) != numTurns {
		t.Fatalf("provider call count = %d, want %d", len(requests), numTurns)
	}
	// Inspect the last request — it carries the full prior history
	// (all numTurns-1 user/assistant pairs from previous turns + the
	// current turn's user message).
	last := requests[len(requests)-1]
	assistantCount := 0
	for _, m := range last.Messages {
		if m.Role != string(RoleAssistant) {
			continue
		}
		assistantCount++
		textParts := 0
		var concat string
		for _, part := range m.Content {
			if tp, ok := part.(types.TextPart); ok {
				textParts++
				concat += tp.Text
			}
		}
		// Same shape as on-disk: 2 collapsed text parts split by the
		// ThinkPart boundary, with total text length == chunksPerTurn*2.
		// If the wire ever regressed and sent 35 chunks the model would
		// have observed the chunked format on the next turn — assertion
		// that the count is exactly 2 (NOT 35+) is what stops the
		// self-perpetuation loop.
		if textParts != 2 {
			t.Fatalf("provider request assistant message #%d: text part count = %d, want 2 collapsed (history bleed-through!)",
				assistantCount, textParts)
		}
		if len(concat) != chunksPerTurn*len(chunkText) {
			t.Fatalf("provider request assistant message #%d: total text length = %d, want %d (chunks should not snowball across turns)",
				assistantCount, len(concat), chunksPerTurn*len(chunkText))
		}
	}
	if assistantCount != numTurns-1 {
		t.Fatalf("provider request assistant message count = %d, want %d (one per prior turn)",
			assistantCount, numTurns-1)
	}
}
