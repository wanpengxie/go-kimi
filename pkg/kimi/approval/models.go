package approval

import "time"

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

// SourceKind identifies where one approval request comes from.
type SourceKind string

const (
	// SourceForegroundTurn identifies one foreground turn source.
	SourceForegroundTurn SourceKind = "foreground_turn"
	// SourceBackgroundAgent identifies one background agent source.
	SourceBackgroundAgent SourceKind = "background_agent"
)

// ApprovalSource tracks one approval request source.
type ApprovalSource struct {
	Kind         SourceKind `json:"kind"`
	ID           string     `json:"id"`
	AgentID      string     `json:"agent_id,omitempty"`
	SubagentType string     `json:"subagent_type,omitempty"`
}

// RequestRecord is one approval request lifecycle record.
type RequestRecord struct {
	ID          string            `json:"id"`
	Source      ApprovalSource    `json:"source"`
	Action      string            `json:"action"`
	Description string            `json:"description"`
	CreatedAt   time.Time         `json:"created_at"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
	Decision    *ApprovalDecision `json:"decision,omitempty"`
	Feedback    string            `json:"feedback,omitempty"`
}

// EventKind identifies one approval runtime event kind.
type EventKind string

const (
	// EventRequestCreated is emitted after one request is created.
	EventRequestCreated EventKind = "request_created"
	// EventRequestResolved is emitted after one request is resolved.
	EventRequestResolved EventKind = "request_resolved"
)

// Event is one approval runtime event payload.
type Event struct {
	Kind   EventKind
	Record *RequestRecord
}
