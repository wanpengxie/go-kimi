package approval

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

func TestApprovalRuntimeConcurrentCreateAndResolve(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	const total = 24

	type concurrentRecord struct {
		id         string
		decision   ApprovalDecision
		feedback   string
		decisionCh <-chan ApprovalDecision
	}

	created := make([]concurrentRecord, total)
	createErrCh := make(chan error, total)
	var createWG sync.WaitGroup

	for i := 0; i < total; i++ {
		i := i
		createWG.Add(1)
		go func() {
			defer createWG.Done()

			source := ApprovalSource{
				Kind: SourceForegroundTurn,
				ID:   fmt.Sprintf("turn-%d", i%4),
			}
			if i%2 == 1 {
				source.Kind = SourceBackgroundAgent
				source.AgentID = fmt.Sprintf("agent-%d", i%3)
				source.SubagentType = "planner"
			}

			decision := ApprovalDecision(i % 3)
			record, decisionCh, err := runtime.CreateRequest(
				context.Background(),
				source,
				fmt.Sprintf("action-%d", i),
				fmt.Sprintf("desc-%d", i),
			)
			if err != nil {
				createErrCh <- fmt.Errorf("CreateRequest[%d] error: %w", i, err)
				return
			}

			created[i] = concurrentRecord{
				id:         record.ID,
				decision:   decision,
				feedback:   fmt.Sprintf("feedback-%d", i),
				decisionCh: decisionCh,
			}
		}()
	}

	createWG.Wait()
	close(createErrCh)
	for err := range createErrCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	if pending := runtime.ListPending(); len(pending) != total {
		t.Fatalf("ListPending() len = %d, want %d", len(pending), total)
	}

	seenIDs := make(map[string]struct{}, total)
	for i := range created {
		if created[i].id == "" {
			t.Fatalf("created[%d].id is empty", i)
		}
		if _, ok := seenIDs[created[i].id]; ok {
			t.Fatalf("duplicate request id: %q", created[i].id)
		}
		seenIDs[created[i].id] = struct{}{}
	}

	resolveErrCh := make(chan error, total)
	var resolveWG sync.WaitGroup
	for i := range created {
		i := i
		resolveWG.Add(1)
		go func() {
			defer resolveWG.Done()
			if err := runtime.Resolve(created[i].id, created[i].decision, "  "+created[i].feedback+"  "); err != nil {
				resolveErrCh <- fmt.Errorf("Resolve[%d] error: %w", i, err)
			}
		}()
	}
	resolveWG.Wait()
	close(resolveErrCh)
	for err := range resolveErrCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	if pending := runtime.ListPending(); len(pending) != 0 {
		t.Fatalf("ListPending() after resolve len = %d, want 0", len(pending))
	}

	for i := range created {
		if decision := waitApprovalDecision(t, created[i].decisionCh); decision != created[i].decision {
			t.Fatalf("decision[%d] = %v, want %v", i, decision, created[i].decision)
		}

		record, err := runtime.GetRequest(created[i].id)
		if err != nil {
			t.Fatalf("GetRequest(%q) error = %v", created[i].id, err)
		}
		if record == nil {
			t.Fatalf("GetRequest(%q) = nil, want non-nil", created[i].id)
		}
		if record.Decision == nil || *record.Decision != created[i].decision {
			t.Fatalf("record[%d].Decision = %#v, want %v", i, record.Decision, created[i].decision)
		}
		if record.ResolvedAt == nil {
			t.Fatalf("record[%d].ResolvedAt = nil, want non-nil", i)
		}
		if record.Feedback != created[i].feedback {
			t.Fatalf("record[%d].Feedback = %q, want %q", i, record.Feedback, created[i].feedback)
		}
	}
}

func TestApprovalRuntimeCancelBySourceAllowsRecreate(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	sub := runtime.Subscribe()

	firstRecord, firstCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceBackgroundAgent, ID: "agent-reuse", AgentID: "agent-reuse", SubagentType: "planner"},
		"shell",
		"first request",
	)
	if err != nil {
		t.Fatalf("CreateRequest(first) error = %v", err)
	}

	firstCreated := waitApprovalEvent(t, sub)
	if firstCreated.Kind != EventRequestCreated || firstCreated.Record == nil || firstCreated.Record.ID != firstRecord.ID {
		t.Fatalf("first created event = %#v, want request_created id=%q", firstCreated, firstRecord.ID)
	}

	canceled := runtime.CancelBySource(SourceKind(" background_agent "), " agent-reuse ")
	if canceled != 1 {
		t.Fatalf("CancelBySource() = %d, want 1", canceled)
	}
	if decision := waitApprovalDecision(t, firstCh); decision != ApprovalReject {
		t.Fatalf("first decision = %v, want reject", decision)
	}

	firstResolved := waitApprovalEvent(t, sub)
	if firstResolved.Kind != EventRequestResolved || firstResolved.Record == nil || firstResolved.Record.ID != firstRecord.ID {
		t.Fatalf("first resolved event = %#v, want request_resolved id=%q", firstResolved, firstRecord.ID)
	}
	if firstResolved.Record.Decision == nil || *firstResolved.Record.Decision != ApprovalReject {
		t.Fatalf("first resolved decision = %#v, want reject", firstResolved.Record.Decision)
	}
	if firstResolved.Record.Feedback != "canceled by source: background_agent/agent-reuse" {
		t.Fatalf("first resolved feedback = %q, want %q", firstResolved.Record.Feedback, "canceled by source: background_agent/agent-reuse")
	}

	storedFirst, err := runtime.GetRequest(firstRecord.ID)
	if err != nil {
		t.Fatalf("GetRequest(first resolved) error = %v", err)
	}
	if storedFirst == nil || storedFirst.Decision == nil || *storedFirst.Decision != ApprovalReject {
		t.Fatalf("GetRequest(first resolved) = %#v, want resolved reject", storedFirst)
	}

	secondRecord, secondCh, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceBackgroundAgent, ID: "agent-reuse", AgentID: "agent-reuse", SubagentType: "planner"},
		"search",
		"second request",
	)
	if err != nil {
		t.Fatalf("CreateRequest(second) error = %v", err)
	}

	secondCreated := waitApprovalEvent(t, sub)
	if secondCreated.Kind != EventRequestCreated || secondCreated.Record == nil || secondCreated.Record.ID != secondRecord.ID {
		t.Fatalf("second created event = %#v, want request_created id=%q", secondCreated, secondRecord.ID)
	}

	pending := runtime.ListPending()
	if len(pending) != 1 || pending[0] == nil || pending[0].ID != secondRecord.ID {
		t.Fatalf("ListPending() = %#v, want only second pending", pending)
	}

	if err := runtime.Resolve(secondRecord.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Resolve(second) error = %v", err)
	}
	if decision := waitApprovalDecision(t, secondCh); decision != ApprovalApprove {
		t.Fatalf("second decision = %v, want approve", decision)
	}

	secondResolved := waitApprovalEvent(t, sub)
	if secondResolved.Kind != EventRequestResolved || secondResolved.Record == nil || secondResolved.Record.ID != secondRecord.ID {
		t.Fatalf("second resolved event = %#v, want request_resolved id=%q", secondResolved, secondRecord.ID)
	}
	if secondResolved.Record.Decision == nil || *secondResolved.Record.Decision != ApprovalApprove {
		t.Fatalf("second resolved decision = %#v, want approve", secondResolved.Record.Decision)
	}
}

func TestApprovalRuntimeEventRecordsAreSnapshots(t *testing.T) {
	t.Parallel()

	runtime := NewApprovalRuntime()
	sub := runtime.Subscribe()

	record, _, err := runtime.CreateRequest(
		context.Background(),
		ApprovalSource{Kind: SourceForegroundTurn, ID: "turn-snapshot"},
		"shell",
		"snapshot case",
	)
	if err != nil {
		t.Fatalf("CreateRequest() error = %v", err)
	}

	created := waitApprovalEvent(t, sub)
	if created.Kind != EventRequestCreated || created.Record == nil || created.Record.ID != record.ID {
		t.Fatalf("created event = %#v, want request_created id=%q", created, record.ID)
	}

	created.Record.Source.ID = "mutated-source"
	created.Record.Action = "mutated-action"
	created.Record.Description = "mutated-description"

	pending, err := runtime.GetRequest(record.ID)
	if err != nil {
		t.Fatalf("GetRequest(pending) error = %v", err)
	}
	if pending.Source.ID != "turn-snapshot" {
		t.Fatalf("pending.Source.ID = %q, want %q", pending.Source.ID, "turn-snapshot")
	}
	if pending.Action != "shell" {
		t.Fatalf("pending.Action = %q, want %q", pending.Action, "shell")
	}
	if pending.Description != "snapshot case" {
		t.Fatalf("pending.Description = %q, want %q", pending.Description, "snapshot case")
	}

	pending.Source.ID = "mutated-from-get"
	reloadedPending, err := runtime.GetRequest(record.ID)
	if err != nil {
		t.Fatalf("GetRequest(reloaded pending) error = %v", err)
	}
	if reloadedPending.Source.ID != "turn-snapshot" {
		t.Fatalf("reloaded pending.Source.ID = %q, want %q", reloadedPending.Source.ID, "turn-snapshot")
	}

	if err := runtime.Resolve(record.ID, ApprovalApproveForSession, "  keep-trimmed  "); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	resolved := waitApprovalEvent(t, sub)
	if resolved.Kind != EventRequestResolved || resolved.Record == nil || resolved.Record.ID != record.ID {
		t.Fatalf("resolved event = %#v, want request_resolved id=%q", resolved, record.ID)
	}

	decision := ApprovalReject
	resolved.Record.Decision = &decision
	resolved.Record.Feedback = "mutated-feedback"

	stored, err := runtime.GetRequest(record.ID)
	if err != nil {
		t.Fatalf("GetRequest(resolved) error = %v", err)
	}
	if stored.Decision == nil || *stored.Decision != ApprovalApproveForSession {
		t.Fatalf("stored.Decision = %#v, want approve_for_session", stored.Decision)
	}
	if stored.Feedback != "keep-trimmed" {
		t.Fatalf("stored.Feedback = %q, want %q", stored.Feedback, "keep-trimmed")
	}
	if stored.ResolvedAt == nil {
		t.Fatal("stored.ResolvedAt = nil, want non-nil")
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
