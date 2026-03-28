//go:build e2e_live

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/shell"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/web"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveContextCompaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	ctxStore := soul.NewSoulContext(t.TempDir())
	wireCh := make(chan wire.WireMessage, 64)
	engine := soul.NewSoul(provider, ctxStore, nil, wire.ChannelEmitter{Ch: wireCh}, "")
	engine.SetCompactionConfig(soul.CompactionConfig{
		Enabled:        true,
		TriggerRatio:   0.2,
		MaxContextSize: 220,
		ReservedSize:   0,
	})
	engine.SetCompactor(&soul.SimpleCompaction{PreserveLastN: 1})

	token := "LIVE_COMPACTION_MEMORY_TOKEN_2026"
	firstPrompt := "Remember this exact token for the conversation: " + token +
		". Reply with EXACT text STORED." +
		" Additional context for compaction load: " + strings.Repeat("context-fragment ", 120)
	firstResult, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: firstPrompt},
	})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if output := strings.TrimSpace(liveTextFromContentParts(firstResult.Content)); output == "" {
		t.Fatalf("first live response is empty: %#v", firstResult.Content)
	}

	secondPrompt := "Now continue with the same context and reply with EXACT text CONTEXT_STILL_OK." +
		" Include no extra words. " + strings.Repeat("second-turn-load ", 80)
	secondResult, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: secondPrompt},
	})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	secondOutput := strings.TrimSpace(liveTextFromContentParts(secondResult.Content))
	if !containsCaseFold(secondOutput, "CONTEXT_STILL_OK") {
		t.Fatalf("second live response = %q, want contains CONTEXT_STILL_OK", secondOutput)
	}

	messages := ctxStore.Messages()
	if len(messages) < 2 {
		t.Fatalf("context message count after compaction flow = %d, want >= 2", len(messages))
	}
	if messages[0].Role != soul.RoleSystem {
		t.Fatalf("first context role after compaction = %q, want system", messages[0].Role)
	}
	if strings.TrimSpace(liveTextFromContentParts(messages[0].Content)) == "" {
		t.Fatalf("compaction summary is empty: %#v", messages[0].Content)
	}

	events := drainLiveWireMessages(wireCh)
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

func TestLiveFetchURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	tool := web.NewFetchURL(nil)
	result, err := tool.Execute(ctx, json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("fetch_url Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("fetch_url result.IsError = true, output=%v", result.Value.Value)
	}

	output, ok := result.Value.Value.(string)
	if !ok {
		t.Fatalf("fetch_url output type = %T, want string", result.Value.Value)
	}
	if !containsCaseFold(output, "Example Domain") {
		t.Fatalf("fetch_url output = %q, want contains Example Domain", output)
	}
}

func TestLiveSoulWithApprovalRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()
	ctxStore := soul.NewSoulContext(t.TempDir())
	runtime := approvalruntime.NewApprovalRuntime()
	runtimeEvents := runtime.Subscribe()
	defer runtime.Unsubscribe(runtimeEvents)

	wireCh := make(chan wire.WireMessage, 64)
	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(shell.New(workDir, nil)),
		wire.ChannelEmitter{Ch: wireCh},
		"When asked to run shell, you must call shell tool exactly once before final answer.",
	)
	engine.SetYolo(false)
	engine.SetApprovalRuntime(runtime, approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   "live-approval-runtime-turn",
	})

	const token = "LIVE_APPROVAL_RUNTIME_TOKEN_2026"
	outcomeCh := make(chan liveRunOutcome, 1)
	go func() {
		result, err := engine.Run(ctx, types.ContentParts{
			types.TextPart{Text: "Call shell tool exactly once with command: printf " + token + ". Then reply with the command output only."},
		})
		outcomeCh <- liveRunOutcome{result: result, err: err}
	}()

	created := waitRuntimeEventByKind(t, runtimeEvents, approvalruntime.EventRequestCreated, 90*time.Second)
	if created.Record == nil || strings.TrimSpace(created.Record.ID) == "" {
		t.Fatalf("created event record = %#v, want non-empty id", created.Record)
	}
	if err := runtime.Resolve(created.Record.ID, approvalruntime.ApprovalApprove, ""); err != nil {
		t.Fatalf("runtime.Resolve(%q) error = %v", created.Record.ID, err)
	}

	resolved := waitRuntimeEventByKind(t, runtimeEvents, approvalruntime.EventRequestResolved, 30*time.Second)
	if resolved.Record == nil || resolved.Record.Decision == nil {
		t.Fatalf("resolved event record = %#v, want resolved decision", resolved.Record)
	}
	if *resolved.Record.Decision != approvalruntime.ApprovalApprove {
		t.Fatalf("resolved decision = %v, want approve", *resolved.Record.Decision)
	}

	outcome := waitLiveRunOutcome(t, outcomeCh, 90*time.Second)
	if outcome.err != nil {
		t.Fatalf("Run() error = %v", outcome.err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(outcome.result.Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
	if !contextHasToolCallName(ctxStore.Messages(), "shell") {
		t.Fatalf("context does not contain shell tool call: %#v", ctxStore.Messages())
	}
	if !contextHasToolOutputContains(ctxStore.Messages(), token) {
		t.Fatalf("context does not contain shell output token %q, messages=%#v", token, ctxStore.Messages())
	}
	if pending := runtime.ListPending(); len(pending) != 0 {
		t.Fatalf("runtime.ListPending() after run = %#v, want empty", pending)
	}
}

type liveRunOutcome struct {
	result soul.StepResult
	err    error
}

func waitLiveRunOutcome(t *testing.T, ch <-chan liveRunOutcome, timeout time.Duration) liveRunOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(timeout):
		t.Fatal("timeout waiting live run outcome")
		return liveRunOutcome{}
	}
}

func waitRuntimeEventByKind(
	t *testing.T,
	ch <-chan approvalruntime.Event,
	kind approvalruntime.EventKind,
	timeout time.Duration,
) approvalruntime.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("runtime event channel closed while waiting %q", kind)
			}
			if event.Kind == kind {
				return event
			}
		case <-time.After(minDuration(remaining, 2*time.Second)):
		}
	}
	t.Fatalf("timeout waiting runtime event kind %q", kind)
	return approvalruntime.Event{}
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return 0
	}
	if a < b {
		return a
	}
	return b
}

func drainLiveWireMessages(ch <-chan wire.WireMessage) []wire.WireMessage {
	messages := make([]wire.WireMessage, 0, cap(ch))
	for {
		select {
		case message := <-ch:
			if message == nil {
				return messages
			}
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

func formatLiveToolOutput(value any) string {
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}
