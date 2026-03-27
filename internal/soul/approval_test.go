package soul

import (
	"context"
	"strings"
	"testing"
	"time"
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
