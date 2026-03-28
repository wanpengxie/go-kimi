//go:build e2e_live

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	kimi "github.com/xiewanpeng/go-kimi/pkg/kimi"
	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/session"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveM8AgentFacadeBasicTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  t.TempDir(),
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	const token = "LIVE_M8_AGENT_BASIC_TOKEN_2026"
	liveM8RunOrSkip(t, agent.Run(ctx, "Reply with this token only: "+token))

	output := strings.TrimSpace(liveTextFromContentParts(agent.LastResult().Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
}

func TestLiveM8AgentFacadeSessionResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()

	agent1, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  workDir,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("NewAgent(first) error = %v", err)
	}

	const firstToken = "LIVE_M8_SESSION_FIRST_TOKEN_2026"
	liveM8RunOrSkip(t, agent1.Run(ctx, "Reply with this token only: "+firstToken))
	firstOutput := strings.TrimSpace(liveTextFromContentParts(agent1.LastResult().Content))
	if !containsCaseFold(firstOutput, firstToken) {
		t.Fatalf("first live response = %q, want contains %q", firstOutput, firstToken)
	}
	if err := agent1.Close(); err != nil {
		t.Fatalf("agent1.Close() error = %v", err)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}

	agent2, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:   workDir,
		SessionID: continued.ID,
		Provider:  provider,
	})
	if err != nil {
		t.Fatalf("NewAgent(second) error = %v", err)
	}

	const secondToken = "LIVE_M8_SESSION_SECOND_TOKEN_2026"
	liveM8RunOrSkip(t, agent2.Run(ctx, "Reply with this token only: "+secondToken))
	secondOutput := strings.TrimSpace(liveTextFromContentParts(agent2.LastResult().Content))
	if !containsCaseFold(secondOutput, secondToken) {
		t.Fatalf("second live response = %q, want contains %q", secondOutput, secondToken)
	}
	if err := agent2.Close(); err != nil {
		t.Fatalf("agent2.Close() error = %v", err)
	}

	ctxStore := soul.NewSoulContext(continued.Dir)
	if err := ctxStore.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore() error = %v", err)
	}
	messages := ctxStore.Messages()
	if len(messages) < 4 {
		t.Fatalf("restored messages = %d, want >= 4", len(messages))
	}
	if messages[0].Role != soul.RoleUser {
		t.Fatalf("restored first role = %q, want user", messages[0].Role)
	}
	if messages[len(messages)-1].Role != soul.RoleAssistant {
		t.Fatalf("restored last role = %q, want assistant", messages[len(messages)-1].Role)
	}
}

func TestLiveM8AgentFacadeApprovalRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	workDir := t.TempDir()
	runtime := approvalruntime.NewApprovalRuntime()
	events := runtime.Subscribe()
	defer runtime.Unsubscribe(events)

	forceYoloOff := false
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:         workDir,
		Provider:        provider,
		ApprovalRuntime: runtime,
		Overrides: kimi.AgentOverrides{
			DefaultYolo: &forceYoloOff,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	const token = "LIVE_M8_APPROVAL_SHELL_TOKEN_2026"
	outcome := make(chan error, 1)
	go func() {
		outcome <- agent.Run(
			ctx,
			"Call shell tool exactly once with command: printf "+token+
				". Then reply with the command output only.",
		)
	}()

	created := liveM8WaitRuntimeEventWithOutcome(
		t,
		events,
		outcome,
		approvalruntime.EventRequestCreated,
		90*time.Second,
	)
	if created.Record == nil || strings.TrimSpace(created.Record.ID) == "" {
		t.Fatalf("created approval event record = %#v, want non-empty id", created.Record)
	}
	if err := runtime.Resolve(created.Record.ID, approvalruntime.ApprovalApprove, ""); err != nil {
		t.Fatalf("runtime.Resolve() error = %v", err)
	}

	resolved := liveM8WaitRuntimeEventWithOutcome(
		t,
		events,
		outcome,
		approvalruntime.EventRequestResolved,
		30*time.Second,
	)
	if resolved.Record == nil || resolved.Record.Decision == nil {
		t.Fatalf("resolved approval event record = %#v, want resolved decision", resolved.Record)
	}
	if *resolved.Record.Decision != approvalruntime.ApprovalApprove {
		t.Fatalf("resolved decision = %v, want approve", *resolved.Record.Decision)
	}

	liveM8RunOrSkip(t, waitLiveM8RunOutcome(t, outcome, 90*time.Second))

	output := strings.TrimSpace(liveTextFromContentParts(agent.LastResult().Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	ctxStore := soul.NewSoulContext(continued.Dir)
	if err := ctxStore.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore() error = %v", err)
	}
	messages := ctxStore.Messages()
	if !contextHasToolCallName(messages, "shell") {
		t.Fatalf("context does not contain shell tool call: %#v", messages)
	}
	if !contextHasToolOutputContains(messages, token) {
		t.Fatalf("context does not contain shell output token %q: %#v", token, messages)
	}
}

func TestLiveM8AgentFacadeWireEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	wireCh := make(chan wire.WireMessage, 128)
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:     t.TempDir(),
		Provider:    provider,
		WireEmitter: wire.ChannelEmitter{Ch: wireCh},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	const token = "LIVE_M8_WIRE_TOKEN_2026"
	liveM8RunOrSkip(t, agent.Run(ctx, "Reply with this token only: "+token))

	events := drainLiveWireMessages(wireCh)
	hasBegin := false
	hasDelta := false
	hasEnd := false
	for i := range events {
		switch events[i].(type) {
		case wire.TurnBegin:
			hasBegin = true
		case wire.TextDelta:
			hasDelta = true
		case wire.TurnEnd:
			hasEnd = true
		}
	}
	if !hasBegin || !hasDelta || !hasEnd {
		t.Fatalf("wire events missing required types: begin=%v delta=%v end=%v events=%#v", hasBegin, hasDelta, hasEnd, events)
	}
}

func liveM8RunOrSkip(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if liveM8IsEnvSkippableError(err) {
		t.Skipf("skip live m8 due env/provider constraint: %v", err)
	}
	t.Fatalf("run error = %v", err)
}

func liveM8IsEnvSkippableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	if strings.Contains(message, "resource_not_found_error") {
		return true
	}
	if strings.Contains(message, "not found the model") {
		return true
	}
	if strings.Contains(message, "permission denied") {
		return true
	}
	if strings.Contains(message, "status 401") || strings.Contains(message, "status 403") {
		return true
	}
	return false
}

func liveM8WaitRuntimeEventWithOutcome(
	t *testing.T,
	events <-chan approvalruntime.Event,
	outcome <-chan error,
	kind approvalruntime.EventKind,
	timeout time.Duration,
) approvalruntime.Event {
	t.Helper()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("runtime event channel closed while waiting %q", kind)
			}
			if event.Kind == kind {
				return event
			}
		case err := <-outcome:
			liveM8RunOrSkip(t, err)
			t.Fatalf("run completed before runtime event %q", kind)
		case <-deadline.C:
			t.Fatalf("timeout waiting runtime event kind %q", kind)
		}
	}
}

func waitLiveM8RunOutcome(t *testing.T, outcome <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-outcome:
		return err
	case <-time.After(timeout):
		t.Fatal("timeout waiting live m8 run outcome")
		return nil
	}
}
