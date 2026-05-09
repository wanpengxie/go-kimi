package soul

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
)

// ApprovalDecision is one approval outcome for one pending request.
type ApprovalDecision = approvalruntime.ApprovalDecision

const (
	// ApprovalApprove approves the current request.
	ApprovalApprove = approvalruntime.ApprovalApprove
	// ApprovalApproveForSession approves current and future requests with same action.
	ApprovalApproveForSession = approvalruntime.ApprovalApproveForSession
	// ApprovalReject rejects the current request.
	ApprovalReject = approvalruntime.ApprovalReject
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
	runtime         *approvalruntime.ApprovalRuntime
	source          approvalruntime.ApprovalSource
}

var approvalRequestSequence uint64
var approvalSourceSequence uint64

// NewApprovalState creates one approval state.
// Default mode is yolo to preserve existing non-blocking tool execution behavior.
func NewApprovalState(onRequest func(*ApprovalRequest)) *ApprovalState {
	return &ApprovalState{
		yolo:            true,
		autoApproved:    map[string]bool{},
		pendingRequests: map[string]*ApprovalRequest{},
		pendingFeedback: map[string]string{},
		onRequest:       onRequest,
		source:          newDefaultApprovalSource(),
	}
}

// Request asks for approval and blocks until one decision arrives or ctx is done.
// It returns (approved, rejectFeedback).
func (a *ApprovalState) Request(ctx context.Context, action, description string) (bool, string) {
	return a.RequestWithToolCallID(ctx, action, description, "")
}

// RequestWithToolCallID asks for approval and associates the request with one tool call id.
func (a *ApprovalState) RequestWithToolCallID(
	ctx context.Context,
	action string,
	description string,
	toolCallID string,
) (bool, string) {
	if a == nil {
		return true, ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	action = strings.TrimSpace(action)
	description = strings.TrimSpace(description)
	toolCallID = strings.TrimSpace(toolCallID)

	a.mu.Lock()
	a.ensureInitializedLocked()
	if a.yolo || a.autoApproved[action] {
		a.mu.Unlock()
		return true, ""
	}
	runtime := a.runtime
	source := a.source
	a.mu.Unlock()

	if runtime != nil {
		return a.requestWithRuntime(ctx, runtime, source, action, description, toolCallID)
	}
	return a.requestLocal(ctx, action, description)
}

func (a *ApprovalState) requestLocal(ctx context.Context, action, description string) (bool, string) {
	request := &ApprovalRequest{
		ID:          newApprovalRequestID(),
		Action:      action,
		Description: description,
		ResponseCh:  make(chan ApprovalDecision, 1),
	}

	a.mu.Lock()
	a.ensureInitializedLocked()
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

func (a *ApprovalState) requestWithRuntime(
	ctx context.Context,
	runtime *approvalruntime.ApprovalRuntime,
	source approvalruntime.ApprovalSource,
	action string,
	description string,
	toolCallID string,
) (bool, string) {
	record, decisionCh, err := runtime.CreateRequestWithToolCall(ctx, source, action, description, toolCallID)
	if err != nil {
		return false, err.Error()
	}

	a.mu.RLock()
	onRequest := a.onRequest
	a.mu.RUnlock()
	if onRequest != nil {
		onRequest(&ApprovalRequest{
			ID:          record.ID,
			Action:      action,
			Description: description,
		})
	}

	select {
	case decision, ok := <-decisionCh:
		if !ok {
			return false, "approval runtime: decision channel closed"
		}
		if decision == ApprovalApproveForSession {
			a.mu.Lock()
			a.ensureInitializedLocked()
			a.autoApproved[action] = true
			a.mu.Unlock()
		}
		feedback := a.consumeFeedback(record.ID)
		if decision == ApprovalReject {
			return false, feedback
		}
		return true, ""
	case <-ctx.Done():
		_ = runtime.Resolve(record.ID, ApprovalReject, ctx.Err().Error())
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

	a.mu.RLock()
	runtime := a.runtime
	a.mu.RUnlock()
	if runtime != nil {
		return a.respondWithRuntime(runtime, requestID, decision, feedback)
	}
	return a.respondLocal(requestID, decision, feedback)
}

func (a *ApprovalState) respondLocal(requestID string, decision ApprovalDecision, feedback string) error {
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

func (a *ApprovalState) respondWithRuntime(
	runtime *approvalruntime.ApprovalRuntime,
	requestID string,
	decision ApprovalDecision,
	feedback string,
) error {
	pending := runtime.ListPending()
	if len(pending) == 0 {
		return fmt.Errorf("approval: request not found: %s", requestID)
	}

	var target *approvalruntime.RequestRecord
	for i := range pending {
		if pending[i] == nil {
			continue
		}
		if pending[i].ID == requestID {
			target = pending[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("approval: request not found: %s", requestID)
	}

	if decision == ApprovalReject {
		a.mu.Lock()
		a.ensureInitializedLocked()
		a.pendingFeedback[requestID] = feedback
		a.mu.Unlock()
	}

	if decision == ApprovalApproveForSession {
		a.mu.Lock()
		a.ensureInitializedLocked()
		a.autoApproved[target.Action] = true
		a.mu.Unlock()

		resolvedTarget := false
		for i := range pending {
			record := pending[i]
			if record == nil {
				continue
			}
			if record.Action != target.Action {
				continue
			}
			if record.Source.Kind != target.Source.Kind || record.Source.ID != target.Source.ID {
				continue
			}

			deliverDecision := ApprovalApprove
			if record.ID == target.ID {
				deliverDecision = ApprovalApproveForSession
				resolvedTarget = true
			}
			if err := runtime.Resolve(record.ID, deliverDecision, ""); err != nil {
				if record.ID == target.ID {
					return mapRuntimeResolveError(record.ID, err)
				}
				if !errors.Is(err, approvalruntime.ErrRequestNotFound) {
					return err
				}
			}
		}

		if !resolvedTarget {
			if err := runtime.Resolve(target.ID, ApprovalApproveForSession, ""); err != nil {
				return mapRuntimeResolveError(target.ID, err)
			}
		}
		return nil
	}

	if err := runtime.Resolve(requestID, decision, feedback); err != nil {
		return mapRuntimeResolveError(requestID, err)
	}
	return nil
}

func mapRuntimeResolveError(requestID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, approvalruntime.ErrRequestNotFound) {
		return fmt.Errorf("approval: request not found: %s", requestID)
	}
	if errors.Is(err, approvalruntime.ErrNilRuntime) {
		return errors.New("approval: runtime unavailable")
	}
	return err
}

// SetRuntime configures one optional centralized approval runtime and source metadata.
func (a *ApprovalState) SetRuntime(runtime *approvalruntime.ApprovalRuntime, source approvalruntime.ApprovalSource) {
	if a == nil {
		return
	}

	normalized := normalizeApprovalSource(source)
	a.mu.Lock()
	a.runtime = runtime
	if normalized.ID != "" {
		a.source = normalized
	} else if a.source.ID == "" {
		a.source = newDefaultApprovalSource()
	}
	a.mu.Unlock()
}

func normalizeApprovalSource(source approvalruntime.ApprovalSource) approvalruntime.ApprovalSource {
	source.Kind = approvalruntime.SourceKind(strings.TrimSpace(string(source.Kind)))
	switch source.Kind {
	case approvalruntime.SourceForegroundTurn, approvalruntime.SourceBackgroundAgent:
	default:
		source.Kind = approvalruntime.SourceForegroundTurn
	}
	source.ID = strings.TrimSpace(source.ID)
	source.AgentID = strings.TrimSpace(source.AgentID)
	source.SubagentType = strings.TrimSpace(source.SubagentType)
	return source
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
	if a.source.ID == "" {
		a.source = newDefaultApprovalSource()
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

func newDefaultApprovalSource() approvalruntime.ApprovalSource {
	return approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   newApprovalSourceID(),
	}
}

func newApprovalSourceID() string {
	sequence := atomic.AddUint64(&approvalSourceSequence, 1)
	return fmt.Sprintf("soul-source-%d-%d", time.Now().UTC().UnixNano(), sequence)
}
