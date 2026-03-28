package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApprovalRuntimeCreateResolveLifecycle(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	sub := runtime.Subscribe()

	record, decisionCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceForegroundTurn, ID: "turn-1"},
		"search",
		"tool=search args={\"q\":\"go\"}",
	)
	if err != nil {
		t.Fatalf("CreateRequest() error = %v", err)
	}
	if record.ID == "" {
		t.Fatal("CreateRequest() record.ID = empty, want non-empty")
	}
	if record.Source.Kind != SourceForegroundTurn || record.Source.ID != "turn-1" {
		t.Fatalf("CreateRequest() source = %#v, want foreground turn-1", record.Source)
	}
	if record.Decision != nil {
		t.Fatalf("CreateRequest() decision = %#v, want nil", record.Decision)
	}
	if record.ResolvedAt != nil {
		t.Fatalf("CreateRequest() resolved_at = %#v, want nil", record.ResolvedAt)
	}

	createdEvent := waitApprovalEvent(t, sub)
	if createdEvent.Kind != EventRequestCreated {
		t.Fatalf("created event kind = %q, want %q", createdEvent.Kind, EventRequestCreated)
	}
	if createdEvent.Record == nil || createdEvent.Record.ID != record.ID {
		t.Fatalf("created event record = %#v, want id=%q", createdEvent.Record, record.ID)
	}

	pending := runtime.ListPending()
	if len(pending) != 1 || pending[0] == nil || pending[0].ID != record.ID {
		t.Fatalf("ListPending() = %#v, want one record id=%q", pending, record.ID)
	}

	if err := runtime.Resolve(record.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	decision := waitApprovalDecision(t, decisionCh)
	if decision != ApprovalApprove {
		t.Fatalf("decision = %v, want %v", decision, ApprovalApprove)
	}

	resolvedEvent := waitApprovalEvent(t, sub)
	if resolvedEvent.Kind != EventRequestResolved {
		t.Fatalf("resolved event kind = %q, want %q", resolvedEvent.Kind, EventRequestResolved)
	}
	if resolvedEvent.Record == nil {
		t.Fatal("resolved event record = nil, want non-nil")
	}
	if resolvedEvent.Record.ID != record.ID {
		t.Fatalf("resolved event record.id = %q, want %q", resolvedEvent.Record.ID, record.ID)
	}
	if resolvedEvent.Record.Decision == nil || *resolvedEvent.Record.Decision != ApprovalApprove {
		t.Fatalf("resolved event decision = %#v, want approve", resolvedEvent.Record.Decision)
	}
	if resolvedEvent.Record.ResolvedAt == nil {
		t.Fatal("resolved event resolved_at = nil, want non-nil")
	}

	if got := runtime.ListPending(); len(got) != 0 {
		t.Fatalf("ListPending() after resolve len = %d, want 0", len(got))
	}
}

func TestApprovalRuntimeCancelBySource(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	_, firstCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceBackgroundAgent, ID: "agent-1", AgentID: "agent-1", SubagentType: "planner"},
		"shell",
		"run shell",
	)
	if err != nil {
		t.Fatalf("CreateRequest(first) error = %v", err)
	}

	_, secondCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceBackgroundAgent, ID: "agent-1", AgentID: "agent-1", SubagentType: "planner"},
		"search",
		"run search",
	)
	if err != nil {
		t.Fatalf("CreateRequest(second) error = %v", err)
	}

	thirdRecord, thirdCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceBackgroundAgent, ID: "agent-2", AgentID: "agent-2", SubagentType: "reviewer"},
		"search",
		"other source",
	)
	if err != nil {
		t.Fatalf("CreateRequest(third) error = %v", err)
	}

	canceled := runtime.CancelBySource(SourceBackgroundAgent, "agent-1")
	if canceled != 2 {
		t.Fatalf("CancelBySource() = %d, want 2", canceled)
	}

	if decision := waitApprovalDecision(t, firstCh); decision != ApprovalReject {
		t.Fatalf("first decision = %v, want reject", decision)
	}
	if decision := waitApprovalDecision(t, secondCh); decision != ApprovalReject {
		t.Fatalf("second decision = %v, want reject", decision)
	}

	select {
	case decision := <-thirdCh:
		t.Fatalf("third decision = %v, want no decision", decision)
	default:
	}

	pending := runtime.ListPending()
	if len(pending) != 1 || pending[0] == nil || pending[0].ID != thirdRecord.ID {
		t.Fatalf("ListPending() after cancel = %#v, want only third record", pending)
	}
}

func TestApprovalRuntimeSubscribeAndUnsubscribe(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	firstSub := runtime.Subscribe()
	secondSub := runtime.Subscribe()

	record, decisionCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceForegroundTurn, ID: "turn-2"},
		"edit",
		"edit README.md",
	)
	if err != nil {
		t.Fatalf("CreateRequest() error = %v", err)
	}

	firstCreated := waitApprovalEvent(t, firstSub)
	secondCreated := waitApprovalEvent(t, secondSub)
	if firstCreated.Kind != EventRequestCreated || secondCreated.Kind != EventRequestCreated {
		t.Fatalf("created event kinds = %q/%q, want both request_created", firstCreated.Kind, secondCreated.Kind)
	}

	runtime.Unsubscribe(firstSub)
	waitApprovalSubscriptionClosed(t, firstSub)

	if err := runtime.Resolve(record.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if decision := waitApprovalDecision(t, decisionCh); decision != ApprovalApprove {
		t.Fatalf("decision = %v, want approve", decision)
	}

	resolved := waitApprovalEvent(t, secondSub)
	if resolved.Kind != EventRequestResolved {
		t.Fatalf("second subscriber resolved kind = %q, want %q", resolved.Kind, EventRequestResolved)
	}
}

func TestApprovalRuntimeCreateRequestValidation(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	if _, _, err := runtime.CreateRequest(context.Background(), ApprovalSource{}, "search", "desc"); err == nil {
		t.Fatal("CreateRequest(source without id) error = nil, want error")
	}

	if err := runtime.Resolve("", ApprovalApprove, ""); err == nil {
		t.Fatal("Resolve(empty id) error = nil, want error")
	}

	if err := runtime.Resolve("missing", ApprovalApprove, ""); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("Resolve(missing) error = %v, want ErrRequestNotFound", err)
	}
}

func TestApprovalRuntimeGetRequestAndToolCallID(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	record, _, err := runtime.CreateRequestWithToolCall(
		context.Background(),
		ApprovalSource{Kind: SourceForegroundTurn, ID: "turn-tool"},
		"shell",
		"run command",
		"call-42",
	)
	if err != nil {
		t.Fatalf("CreateRequestWithToolCall() error = %v", err)
	}

	pending, err := runtime.GetRequest(record.ID)
	if err != nil {
		t.Fatalf("GetRequest(pending) error = %v", err)
	}
	if pending == nil {
		t.Fatal("GetRequest(pending) = nil, want non-nil")
	}
	if pending.ToolCallID != "call-42" {
		t.Fatalf("pending.ToolCallID = %q, want %q", pending.ToolCallID, "call-42")
	}
	if pending.ResolvedAt != nil {
		t.Fatalf("pending.ResolvedAt = %#v, want nil", pending.ResolvedAt)
	}

	if err := runtime.Resolve(record.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	resolved, err := runtime.GetRequest(record.ID)
	if err != nil {
		t.Fatalf("GetRequest(resolved) error = %v", err)
	}
	if resolved == nil || resolved.Decision == nil || *resolved.Decision != ApprovalApprove {
		t.Fatalf("GetRequest(resolved) decision = %#v, want approve", resolved)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("GetRequest(resolved).ResolvedAt = nil, want non-nil")
	}
	if resolved.ToolCallID != "call-42" {
		t.Fatalf("resolved.ToolCallID = %q, want %q", resolved.ToolCallID, "call-42")
	}

	if _, err := runtime.GetRequest("missing"); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("GetRequest(missing) error = %v, want ErrRequestNotFound", err)
	}
}

func waitApprovalEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatal("approval event channel closed unexpectedly")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval event")
		return Event{}
	}
}

func waitApprovalDecision(t *testing.T, ch <-chan ApprovalDecision) ApprovalDecision {
	t.Helper()
	select {
	case decision, ok := <-ch:
		if !ok {
			t.Fatal("approval decision channel closed unexpectedly")
		}
		return decision
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval decision")
		return ApprovalReject
	}
}

func waitApprovalSubscriptionClosed(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscription channel still open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting subscription close")
	}
}
