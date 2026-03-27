package soul

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ApprovalDecision is one approval outcome for one pending request.
type ApprovalDecision int

const (
	// ApprovalApprove approves the current request.
	ApprovalApprove ApprovalDecision = iota
	// ApprovalApproveForSession approves current and future requests with same action.
	ApprovalApproveForSession
	// ApprovalReject rejects the current request.
	ApprovalReject
)

// ApprovalRequest represents one external approval prompt.
type ApprovalRequest struct {
	ID          string
	Action      string
	Description string
	ResponseCh  chan ApprovalDecision
}

// ApprovalState tracks one turn/session approval state.
type ApprovalState struct {
	yolo bool

	autoApproved map[string]bool

	mu              sync.RWMutex
	pendingRequests map[string]*ApprovalRequest
	pendingFeedback map[string]string
	onRequest       func(*ApprovalRequest)
}

var approvalRequestSequence uint64

// NewApprovalState creates one approval state.
// Default mode is yolo to preserve existing non-blocking tool execution behavior.
func NewApprovalState(onRequest func(*ApprovalRequest)) *ApprovalState {
	return &ApprovalState{
		yolo:            true,
		autoApproved:    map[string]bool{},
		pendingRequests: map[string]*ApprovalRequest{},
		pendingFeedback: map[string]string{},
		onRequest:       onRequest,
	}
}

// Request asks for approval and blocks until one decision arrives or ctx is done.
// It returns (approved, rejectFeedback).
func (a *ApprovalState) Request(ctx context.Context, action, description string) (bool, string) {
	if a == nil {
		return true, ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	action = strings.TrimSpace(action)
	description = strings.TrimSpace(description)
	request := &ApprovalRequest{
		ID:          newApprovalRequestID(),
		Action:      action,
		Description: description,
		ResponseCh:  make(chan ApprovalDecision, 1),
	}

	a.mu.Lock()
	a.ensureInitializedLocked()
	if a.yolo || a.autoApproved[action] {
		a.mu.Unlock()
		return true, ""
	}

	a.pendingRequests[request.ID] = request
	onRequest := a.onRequest
	a.mu.Unlock()

	if onRequest != nil {
		onRequest(request)
	}

	select {
	case decision := <-request.ResponseCh:
		feedback := a.consumeFeedback(request.ID)
		if decision == ApprovalReject {
			return false, feedback
		}
		return true, ""
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.pendingRequests, request.ID)
		delete(a.pendingFeedback, request.ID)
		a.mu.Unlock()
		return false, ctx.Err().Error()
	}
}

// Respond resolves one pending request by id.
func (a *ApprovalState) Respond(requestID string, decision ApprovalDecision, feedback string) error {
	if a == nil {
		return errors.New("approval: nil state")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return errors.New("approval: empty request id")
	}
	if !isValidDecision(decision) {
		return fmt.Errorf("approval: invalid decision: %d", decision)
	}

	feedback = strings.TrimSpace(feedback)

	a.mu.Lock()
	a.ensureInitializedLocked()

	request, ok := a.pendingRequests[requestID]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("approval: request not found: %s", requestID)
	}

	targets := []*ApprovalRequest{request}
	delete(a.pendingRequests, requestID)

	if decision == ApprovalReject {
		a.pendingFeedback[requestID] = feedback
	}

	if decision == ApprovalApproveForSession {
		a.autoApproved[request.Action] = true
		for id, pending := range a.pendingRequests {
			if pending.Action != request.Action {
				continue
			}
			delete(a.pendingRequests, id)
			targets = append(targets, pending)
		}
	}
	a.mu.Unlock()

	for idx := range targets {
		deliverDecision := decision
		if decision == ApprovalApproveForSession && idx > 0 {
			deliverDecision = ApprovalApprove
		}

		// Buffered response channel prevents deadlock when receiver already exited.
		select {
		case targets[idx].ResponseCh <- deliverDecision:
		default:
		}
	}
	return nil
}

// SetYolo toggles yolo mode.
func (a *ApprovalState) SetYolo(v bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.yolo = v
	a.mu.Unlock()
}

// IsYolo returns whether yolo mode is enabled.
func (a *ApprovalState) IsYolo() bool {
	if a == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.yolo
}

func (a *ApprovalState) ensureInitializedLocked() {
	if a.autoApproved == nil {
		a.autoApproved = map[string]bool{}
	}
	if a.pendingRequests == nil {
		a.pendingRequests = map[string]*ApprovalRequest{}
	}
	if a.pendingFeedback == nil {
		a.pendingFeedback = map[string]string{}
	}
}

func (a *ApprovalState) consumeFeedback(requestID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	feedback, ok := a.pendingFeedback[requestID]
	if !ok {
		return ""
	}
	delete(a.pendingFeedback, requestID)
	return feedback
}

func isValidDecision(decision ApprovalDecision) bool {
	switch decision {
	case ApprovalApprove, ApprovalApproveForSession, ApprovalReject:
		return true
	default:
		return false
	}
}

func newApprovalRequestID() string {
	sequence := atomic.AddUint64(&approvalRequestSequence, 1)
	return fmt.Sprintf("approval-%d-%d", time.Now().UTC().UnixNano(), sequence)
}
