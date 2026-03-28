package approval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var runtimeRequestSequence uint64

var (
	// ErrNilRuntime reports one nil runtime receiver usage.
	ErrNilRuntime = errors.New("approval runtime: nil runtime")
	// ErrRequestNotFound reports one missing pending request.
	ErrRequestNotFound = errors.New("approval runtime: request not found")
)

// ApprovalRuntime is one centralized approval broker.
type ApprovalRuntime struct {
	mu sync.RWMutex

	pending     map[string]*pendingRequest
	resolved    map[string]*RequestRecord
	subscribers map[<-chan Event]chan Event
}

type pendingRequest struct {
	record     RequestRecord
	decisionCh chan ApprovalDecision
}

// NewApprovalRuntime creates one runtime instance.
func NewApprovalRuntime() *ApprovalRuntime {
	return &ApprovalRuntime{
		pending:     make(map[string]*pendingRequest),
		resolved:    make(map[string]*RequestRecord),
		subscribers: make(map[<-chan Event]chan Event),
	}
}

// CreateRequest creates one pending approval request and returns one decision channel.
func (r *ApprovalRuntime) CreateRequest(
	ctx context.Context,
	source ApprovalSource,
	action string,
	description string,
) (RequestRecord, <-chan ApprovalDecision, error) {
	return r.CreateRequestWithToolCall(ctx, source, action, description, "")
}

// CreateRequestWithToolCall creates one pending approval request and associates one tool_call_id.
func (r *ApprovalRuntime) CreateRequestWithToolCall(
	ctx context.Context,
	source ApprovalSource,
	action string,
	description string,
	toolCallID string,
) (RequestRecord, <-chan ApprovalDecision, error) {
	if r == nil {
		return RequestRecord{}, nil, ErrNilRuntime
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-ctx.Done():
		return RequestRecord{}, nil, fmt.Errorf("approval runtime: create request: %w", ctx.Err())
	default:
	}

	normalizedSource, err := normalizeSource(source)
	if err != nil {
		return RequestRecord{}, nil, err
	}

	record := RequestRecord{
		ID:          newRuntimeRequestID(),
		Source:      normalizedSource,
		Action:      strings.TrimSpace(action),
		Description: strings.TrimSpace(description),
		ToolCallID:  strings.TrimSpace(toolCallID),
		CreatedAt:   time.Now().UTC(),
	}

	decisionCh := make(chan ApprovalDecision, 1)

	r.mu.Lock()
	r.ensureInitializedLocked()
	r.pending[record.ID] = &pendingRequest{
		record:     record,
		decisionCh: decisionCh,
	}
	r.mu.Unlock()

	r.publishEvent(Event{
		Kind:   EventRequestCreated,
		Record: cloneRequestRecord(&record),
	})

	return record, decisionCh, nil
}

// Resolve resolves one pending request by id.
func (r *ApprovalRuntime) Resolve(requestID string, decision ApprovalDecision, feedback string) error {
	if r == nil {
		return ErrNilRuntime
	}

	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return errors.New("approval runtime: empty request id")
	}
	if !isValidDecision(decision) {
		return fmt.Errorf("approval runtime: invalid decision: %d", decision)
	}

	feedback = strings.TrimSpace(feedback)

	r.mu.Lock()
	r.ensureInitializedLocked()
	pending, ok := r.pending[normalizedRequestID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRequestNotFound, normalizedRequestID)
	}
	delete(r.pending, normalizedRequestID)

	resolvedAt := time.Now().UTC()
	record := pending.record
	record.ResolvedAt = &resolvedAt
	recordedDecision := decision
	record.Decision = &recordedDecision
	record.Feedback = feedback
	r.resolved[record.ID] = cloneRequestRecord(&record)
	r.mu.Unlock()

	select {
	case pending.decisionCh <- decision:
	default:
	}
	close(pending.decisionCh)

	r.publishEvent(Event{
		Kind:   EventRequestResolved,
		Record: cloneRequestRecord(&record),
	})
	return nil
}

// CancelBySource resolves all pending requests for one source as rejected.
func (r *ApprovalRuntime) CancelBySource(kind SourceKind, sourceID string) int {
	if r == nil {
		return 0
	}

	normalizedKind := normalizeSourceKind(kind)
	normalizedSourceID := strings.TrimSpace(sourceID)
	if normalizedKind == "" || normalizedSourceID == "" {
		return 0
	}

	r.mu.Lock()
	r.ensureInitializedLocked()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return 0
	}

	matched := make([]*pendingRequest, 0, len(r.pending))
	for requestID, pending := range r.pending {
		if pending.record.Source.Kind != normalizedKind || pending.record.Source.ID != normalizedSourceID {
			continue
		}
		matched = append(matched, pending)
		delete(r.pending, requestID)
	}
	r.mu.Unlock()

	if len(matched) == 0 {
		return 0
	}

	feedback := fmt.Sprintf("canceled by source: %s/%s", normalizedKind, normalizedSourceID)
	for i := range matched {
		record := matched[i].record
		resolvedAt := time.Now().UTC()
		record.ResolvedAt = &resolvedAt
		decision := ApprovalReject
		record.Decision = &decision
		record.Feedback = feedback
		r.mu.Lock()
		r.ensureInitializedLocked()
		r.resolved[record.ID] = cloneRequestRecord(&record)
		r.mu.Unlock()

		select {
		case matched[i].decisionCh <- ApprovalReject:
		default:
		}
		close(matched[i].decisionCh)

		r.publishEvent(Event{
			Kind:   EventRequestResolved,
			Record: cloneRequestRecord(&record),
		})
	}
	return len(matched)
}

// ListPending returns one snapshot of current pending requests.
func (r *ApprovalRuntime) ListPending() []*RequestRecord {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	if len(r.pending) == 0 {
		r.mu.RUnlock()
		return nil
	}

	records := make([]*RequestRecord, 0, len(r.pending))
	for _, pending := range r.pending {
		records = append(records, cloneRequestRecord(&pending.record))
	}
	r.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool {
		left := records[i]
		right := records[j]
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	return records
}

// GetRequest returns one request record by id from pending or resolved snapshots.
func (r *ApprovalRuntime) GetRequest(requestID string) (*RequestRecord, error) {
	if r == nil {
		return nil, ErrNilRuntime
	}

	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return nil, errors.New("approval runtime: empty request id")
	}

	r.mu.RLock()
	if pending, ok := r.pending[normalizedRequestID]; ok && pending != nil {
		record := cloneRequestRecord(&pending.record)
		r.mu.RUnlock()
		return record, nil
	}
	if resolved, ok := r.resolved[normalizedRequestID]; ok {
		record := cloneRequestRecord(resolved)
		r.mu.RUnlock()
		return record, nil
	}
	r.mu.RUnlock()
	return nil, fmt.Errorf("%w: %s", ErrRequestNotFound, normalizedRequestID)
}

// Subscribe subscribes runtime events.
func (r *ApprovalRuntime) Subscribe() <-chan Event {
	if r == nil {
		ch := make(chan Event)
		close(ch)
		return ch
	}

	sendCh := make(chan Event, 16)
	recvCh := (<-chan Event)(sendCh)

	r.mu.Lock()
	r.ensureInitializedLocked()
	r.subscribers[recvCh] = sendCh
	r.mu.Unlock()
	return recvCh
}

// Unsubscribe removes one prior subscription.
func (r *ApprovalRuntime) Unsubscribe(ch <-chan Event) {
	if r == nil || ch == nil {
		return
	}

	r.mu.Lock()
	sendCh, ok := r.subscribers[ch]
	if ok {
		delete(r.subscribers, ch)
	}
	r.mu.Unlock()

	if ok {
		close(sendCh)
	}
}

func (r *ApprovalRuntime) ensureInitializedLocked() {
	if r.pending == nil {
		r.pending = make(map[string]*pendingRequest)
	}
	if r.resolved == nil {
		r.resolved = make(map[string]*RequestRecord)
	}
	if r.subscribers == nil {
		r.subscribers = make(map[<-chan Event]chan Event)
	}
}

func (r *ApprovalRuntime) publishEvent(event Event) {
	if r == nil {
		return
	}

	r.mu.RLock()
	for _, sendCh := range r.subscribers {
		eventCopy := cloneEvent(event)
		select {
		case sendCh <- eventCopy:
		default:
		}
	}
	r.mu.RUnlock()
}

func normalizeSource(source ApprovalSource) (ApprovalSource, error) {
	source.Kind = normalizeSourceKind(source.Kind)
	if source.Kind == "" {
		source.Kind = SourceForegroundTurn
	}

	source.ID = strings.TrimSpace(source.ID)
	if source.ID == "" {
		return ApprovalSource{}, errors.New("approval runtime: source id is required")
	}

	source.AgentID = strings.TrimSpace(source.AgentID)
	source.SubagentType = strings.TrimSpace(source.SubagentType)
	return source, nil
}

func normalizeSourceKind(kind SourceKind) SourceKind {
	switch SourceKind(strings.TrimSpace(string(kind))) {
	case SourceForegroundTurn:
		return SourceForegroundTurn
	case SourceBackgroundAgent:
		return SourceBackgroundAgent
	default:
		return ""
	}
}

func isValidDecision(decision ApprovalDecision) bool {
	switch decision {
	case ApprovalApprove, ApprovalApproveForSession, ApprovalReject:
		return true
	default:
		return false
	}
}

func newRuntimeRequestID() string {
	sequence := atomic.AddUint64(&runtimeRequestSequence, 1)
	return fmt.Sprintf("approval-%d-%d", time.Now().UTC().UnixNano(), sequence)
}

func cloneRequestRecord(record *RequestRecord) *RequestRecord {
	if record == nil {
		return nil
	}
	copyRecord := *record
	if record.ResolvedAt != nil {
		resolvedAt := *record.ResolvedAt
		copyRecord.ResolvedAt = &resolvedAt
	}
	if record.Decision != nil {
		decision := *record.Decision
		copyRecord.Decision = &decision
	}
	return &copyRecord
}

func cloneEvent(event Event) Event {
	return Event{
		Kind:   event.Kind,
		Record: cloneRequestRecord(event.Record),
	}
}
