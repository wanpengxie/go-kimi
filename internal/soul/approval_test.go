package soul

import (
	"context"
	"strings"
	"testing"
	"time"

	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
)

func TestApprovalStateRequestYolo(t *testing.T) {
	t.Parallel()

	state := NewApprovalState(nil)
	approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"go\"}")
	if !approved {
		t.Fatalf("Request() approved = false, want true")
	}
	if feedback != "" {
		t.Fatalf("Request() feedback = %q, want empty", feedback)
	}
}

func TestApprovalStateRequestBlocksUntilRespond(t *testing.T) {
	t.Parallel()

	requestCh := make(chan *ApprovalRequest, 1)
	state := NewApprovalState(func(request *ApprovalRequest) {
		requestCh <- request
	})
	state.SetYolo(false)

	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 1)
	go func() {
		approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"go\"}")
		outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
	}()

	var request *ApprovalRequest
	select {
	case request = <-requestCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval request")
	}

	select {
	case <-outcomeCh:
		t.Fatal("Request() returned before Respond()")
	default:
	}

	if err := state.Respond(request.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	select {
	case outcome := <-outcomeCh:
		if !outcome.approved {
			t.Fatalf("Request() approved = false, want true")
		}
		if outcome.feedback != "" {
			t.Fatalf("Request() feedback = %q, want empty", outcome.feedback)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Request() completion")
	}
}

func TestApprovalStateApproveForSessionAutoApprovesPending(t *testing.T) {
	t.Parallel()

	requestCh := make(chan *ApprovalRequest, 4)
	state := NewApprovalState(func(request *ApprovalRequest) {
		requestCh <- request
	})
	state.SetYolo(false)

	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 2)
	startRequest := func() {
		go func() {
			approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"go\"}")
			outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
		}()
	}

	startRequest()
	startRequest()

	var firstRequest *ApprovalRequest
	select {
	case firstRequest = <-requestCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting first approval request")
	}

	select {
	case <-requestCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting second approval request")
	}

	if err := state.Respond(firstRequest.ID, ApprovalApproveForSession, ""); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case outcome := <-outcomeCh:
			if !outcome.approved {
				t.Fatalf("outcome[%d].approved = false, want true", i)
			}
			if outcome.feedback != "" {
				t.Fatalf("outcome[%d].feedback = %q, want empty", i, outcome.feedback)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting approval outcome[%d]", i)
		}
	}

	approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"next\"}")
	if !approved {
		t.Fatalf("Request() after ApproveForSession approved = false, want true")
	}
	if feedback != "" {
		t.Fatalf("Request() after ApproveForSession feedback = %q, want empty", feedback)
	}

	select {
	case unexpected := <-requestCh:
		t.Fatalf("unexpected approval request emitted after session auto-approve: %q", unexpected.ID)
	default:
	}
}

func TestApprovalStateRequestContextDone(t *testing.T) {
	t.Parallel()

	requestCh := make(chan *ApprovalRequest, 1)
	state := NewApprovalState(func(request *ApprovalRequest) {
		requestCh <- request
	})
	state.SetYolo(false)

	ctx, cancel := context.WithCancel(context.Background())
	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 1)
	go func() {
		approved, feedback := state.Request(ctx, "edit", "tool=edit args={\"path\":\"README.md\"}")
		outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
	}()

	var request *ApprovalRequest
	select {
	case request = <-requestCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval request")
	}

	cancel()

	select {
	case outcome := <-outcomeCh:
		if outcome.approved {
			t.Fatalf("Request() approved = true, want false")
		}
		if !strings.Contains(outcome.feedback, context.Canceled.Error()) {
			t.Fatalf("Request() feedback = %q, want contains %q", outcome.feedback, context.Canceled.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Request() cancel return")
	}

	if err := state.Respond(request.ID, ApprovalApprove, ""); err == nil {
		t.Fatalf("Respond() after cancel error = nil, want non-nil")
	}
}

func TestApprovalStateRequestDelegatesToRuntime(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()
	sub := runtime.Subscribe()

	state := NewApprovalState(nil)
	state.SetYolo(false)
	state.SetRuntime(runtime, approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   "turn-1",
	})

	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 1)
	go func() {
		approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"go\"}")
		outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
	}()

	var created approvalruntime.Event
	select {
	case created = <-sub:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting runtime created event")
	}
	if created.Kind != approvalruntime.EventRequestCreated {
		t.Fatalf("runtime created kind = %q, want %q", created.Kind, approvalruntime.EventRequestCreated)
	}
	if created.Record == nil {
		t.Fatal("runtime created record = nil, want non-nil")
	}
	if created.Record.Source.Kind != approvalruntime.SourceForegroundTurn || created.Record.Source.ID != "turn-1" {
		t.Fatalf("runtime created source = %#v, want foreground turn-1", created.Record.Source)
	}

	if err := state.Respond(created.Record.ID, ApprovalApprove, ""); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	select {
	case outcome := <-outcomeCh:
		if !outcome.approved {
			t.Fatalf("Request() approved = false, want true")
		}
		if outcome.feedback != "" {
			t.Fatalf("Request() feedback = %q, want empty", outcome.feedback)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting Request() completion")
	}
}

func TestApprovalStateApproveForSessionWithRuntime(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()
	state := NewApprovalState(nil)
	state.SetYolo(false)
	state.SetRuntime(runtime, approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   "turn-approve-session",
	})

	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 2)
	startRequest := func() {
		go func() {
			approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"go\"}")
			outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
		}()
	}
	startRequest()
	startRequest()

	var firstID string
	deadline := time.Now().Add(time.Second)
	for {
		pending := runtime.ListPending()
		if len(pending) >= 2 {
			firstID = pending[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime pending count timeout, got=%d want>=2", len(pending))
		}
		time.Sleep(time.Millisecond)
	}

	if err := state.Respond(firstID, ApprovalApproveForSession, ""); err != nil {
		t.Fatalf("Respond(ApproveForSession) error = %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case outcome := <-outcomeCh:
			if !outcome.approved {
				t.Fatalf("outcome[%d].approved = false, want true", i)
			}
			if outcome.feedback != "" {
				t.Fatalf("outcome[%d].feedback = %q, want empty", i, outcome.feedback)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting outcome[%d]", i)
		}
	}

	approved, feedback := state.Request(context.Background(), "search", "tool=search args={\"q\":\"next\"}")
	if !approved {
		t.Fatalf("Request() after ApproveForSession approved = false, want true")
	}
	if feedback != "" {
		t.Fatalf("Request() after ApproveForSession feedback = %q, want empty", feedback)
	}
}

func TestApprovalStateRequestRuntimeContextDoneCleansPending(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()
	state := NewApprovalState(nil)
	state.SetYolo(false)
	state.SetRuntime(runtime, approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   "turn-cancel",
	})

	ctx, cancel := context.WithCancel(context.Background())
	type approvalOutcome struct {
		approved bool
		feedback string
	}
	outcomeCh := make(chan approvalOutcome, 1)
	go func() {
		approved, feedback := state.Request(ctx, "edit", "tool=edit args={\"path\":\"README.md\"}")
		outcomeCh <- approvalOutcome{approved: approved, feedback: feedback}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if len(runtime.ListPending()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting runtime pending request")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()

	select {
	case outcome := <-outcomeCh:
		if outcome.approved {
			t.Fatalf("Request() approved = true, want false")
		}
		if !strings.Contains(outcome.feedback, context.Canceled.Error()) {
			t.Fatalf("Request() feedback = %q, want contains %q", outcome.feedback, context.Canceled.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting Request() cancel return")
	}

	deadline = time.Now().Add(time.Second)
	for {
		if len(runtime.ListPending()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime pending requests not cleaned, got=%d", len(runtime.ListPending()))
		}
		time.Sleep(time.Millisecond)
	}
}
