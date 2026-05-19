package soul

import (
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
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
