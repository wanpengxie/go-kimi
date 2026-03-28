package wire

import "github.com/xiewanpeng/go-kimi/pkg/kimi/types"

// WireMessageType identifies the concrete message variant carried by an envelope.
type WireMessageType string

const (
	WireMessageTypeTurnBegin       WireMessageType = "turn_begin"
	WireMessageTypeTextDelta       WireMessageType = "text_delta"
	WireMessageTypeSteerInput      WireMessageType = "steer_input"
	WireMessageTypeTurnEnd         WireMessageType = "turn_end"
	WireMessageTypeStepBegin       WireMessageType = "step_begin"
	WireMessageTypeStepInterrupted WireMessageType = "step_interrupted"
	WireMessageTypeCompactionBegin WireMessageType = "compaction_begin"
	WireMessageTypeCompactionError WireMessageType = "compaction_error"
	WireMessageTypeCompactionEnd   WireMessageType = "compaction_end"
	WireMessageTypeMCPLoadingBegin WireMessageType = "mcp_loading_begin"
	WireMessageTypeMCPLoadingEnd   WireMessageType = "mcp_loading_end"
	WireMessageTypeStatusUpdate    WireMessageType = "status_update"
	WireMessageTypeNotification    WireMessageType = "notification"
	WireMessageTypeSubagentEvent   WireMessageType = "subagent_event"

	WireMessageTypeApprovalRequest  WireMessageType = "approval_request"
	WireMessageTypeApprovalResponse WireMessageType = "approval_response"
	WireMessageTypeToolCallRequest  WireMessageType = "tool_call_request"
	WireMessageTypeToolCallResult   WireMessageType = "tool_call_result"
	WireMessageTypeQuestionRequest  WireMessageType = "question_request"
	WireMessageTypeQuestionResponse WireMessageType = "question_response"
	WireMessageTypeQuestionOption   WireMessageType = "question_option"
	WireMessageTypeQuestionItem     WireMessageType = "question_item"
)

// WireMessage is any event/request payload that can be wrapped in a wire envelope.
type WireMessage interface {
	wireMessageType() WireMessageType
}

// Event is a one-way runtime notification payload.
type Event interface {
	WireMessage
	IsEvent()
}

// Request is a payload that expects a response or user action.
type Request interface {
	WireMessage
	IsRequest()
}

// MCPStatusSnapshot captures one MCP server runtime status snapshot.
type MCPStatusSnapshot struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Disabled bool   `json:"disabled,omitempty"`
	Message  string `json:"message,omitempty"`
}

// MCPServerSnapshot captures all MCP server states at a point in time.
type MCPServerSnapshot struct {
	Servers []MCPStatusSnapshot `json:"servers"`
}

// TurnBegin marks the start of a turn.
type TurnBegin struct {
	TurnID string             `json:"turn_id"`
	Input  types.ContentParts `json:"input,omitempty"`
}

func (TurnBegin) IsEvent() {}

func (TurnBegin) wireMessageType() WireMessageType {
	return WireMessageTypeTurnBegin
}

// TextDelta carries one streamed text increment for a turn.
type TextDelta struct {
	TurnID string `json:"turn_id,omitempty"`
	Delta  string `json:"delta"`
}

func (TextDelta) IsEvent() {}

func (TextDelta) wireMessageType() WireMessageType {
	return WireMessageTypeTextDelta
}

// SteerInput carries steering input for an in-progress turn.
type SteerInput struct {
	Text     string `json:"text"`
	Priority string `json:"priority,omitempty"`
}

func (SteerInput) IsEvent() {}

func (SteerInput) wireMessageType() WireMessageType {
	return WireMessageTypeSteerInput
}

// TurnEnd marks the end of a turn.
type TurnEnd struct {
	TurnID      string             `json:"turn_id,omitempty"`
	StopReason  string             `json:"stop_reason,omitempty"`
	Output      types.ContentParts `json:"output,omitempty"`
	Usage       *types.TokenUsage  `json:"usage,omitempty"`
	Interrupted bool               `json:"interrupted,omitempty"`
}

func (TurnEnd) IsEvent() {}

func (TurnEnd) wireMessageType() WireMessageType {
	return WireMessageTypeTurnEnd
}

// StepBegin marks the beginning of an execution step.
type StepBegin struct {
	StepID      string `json:"step_id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

func (StepBegin) IsEvent() {}

func (StepBegin) wireMessageType() WireMessageType {
	return WireMessageTypeStepBegin
}

// StepInterrupted reports an interrupted execution step.
type StepInterrupted struct {
	StepID string `json:"step_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (StepInterrupted) IsEvent() {}

func (StepInterrupted) wireMessageType() WireMessageType {
	return WireMessageTypeStepInterrupted
}

// CompactionBegin signals context compaction start.
type CompactionBegin struct {
	Trigger string `json:"trigger,omitempty"`
}

func (CompactionBegin) IsEvent() {}

func (CompactionBegin) wireMessageType() WireMessageType {
	return WireMessageTypeCompactionBegin
}

// CompactionError signals one recoverable context compaction failure.
type CompactionError struct {
	Error string `json:"error,omitempty"`
}

func (CompactionError) IsEvent() {}

func (CompactionError) wireMessageType() WireMessageType {
	return WireMessageTypeCompactionError
}

// CompactionEnd signals context compaction completion.
type CompactionEnd struct {
	Summary string             `json:"summary,omitempty"`
	Content types.ContentParts `json:"content,omitempty"`
}

func (CompactionEnd) IsEvent() {}

func (CompactionEnd) wireMessageType() WireMessageType {
	return WireMessageTypeCompactionEnd
}

// MCPLoadingBegin signals the start of MCP loading.
type MCPLoadingBegin struct {
	Snapshot *MCPServerSnapshot `json:"snapshot,omitempty"`
}

func (MCPLoadingBegin) IsEvent() {}

func (MCPLoadingBegin) wireMessageType() WireMessageType {
	return WireMessageTypeMCPLoadingBegin
}

// MCPLoadingEnd signals the completion of MCP loading.
type MCPLoadingEnd struct {
	Snapshot   *MCPServerSnapshot `json:"snapshot,omitempty"`
	DurationMS int64              `json:"duration_ms,omitempty"`
}

func (MCPLoadingEnd) IsEvent() {}

func (MCPLoadingEnd) wireMessageType() WireMessageType {
	return WireMessageTypeMCPLoadingEnd
}

// StatusUpdate reports a coarse runtime state update.
type StatusUpdate struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (StatusUpdate) IsEvent() {}

func (StatusUpdate) wireMessageType() WireMessageType {
	return WireMessageTypeStatusUpdate
}

// Notification carries user-facing notification content.
type Notification struct {
	Level   string              `json:"level,omitempty"`
	Message string              `json:"message"`
	Blocks  types.DisplayBlocks `json:"blocks,omitempty"`
}

func (Notification) IsEvent() {}

func (Notification) wireMessageType() WireMessageType {
	return WireMessageTypeNotification
}

// SubagentEvent reports a subagent lifecycle or output event.
type SubagentEvent struct {
	AgentID   string         `json:"agent_id"`
	EventType string         `json:"event_type"`
	Message   string         `json:"message,omitempty"`
	Payload   types.JsonType `json:"payload,omitempty"`
}

func (SubagentEvent) IsEvent() {}

func (SubagentEvent) wireMessageType() WireMessageType {
	return WireMessageTypeSubagentEvent
}

// ApprovalRequest asks the host to approve a sensitive action.
type ApprovalRequest struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind,omitempty"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Command     string         `json:"command,omitempty"`
	Arguments   []string       `json:"arguments,omitempty"`
	Metadata    types.JsonType `json:"metadata,omitempty"`
}

func (ApprovalRequest) IsRequest() {}

func (ApprovalRequest) wireMessageType() WireMessageType {
	return WireMessageTypeApprovalRequest
}

// ApprovalResponse reports an approval decision.
type ApprovalResponse struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
	Reason    string `json:"reason,omitempty"`
}

func (ApprovalResponse) IsRequest() {}

func (ApprovalResponse) wireMessageType() WireMessageType {
	return WireMessageTypeApprovalResponse
}

// ToolCallRequest asks the host runtime to execute a tool call.
type ToolCallRequest struct {
	ID       string         `json:"id,omitempty"`
	ToolCall types.ToolCall `json:"tool_call"`
}

func (ToolCallRequest) IsRequest() {}

func (ToolCallRequest) wireMessageType() WireMessageType {
	return WireMessageTypeToolCallRequest
}

// ToolCallResult reports one tool execution result.
type ToolCallResult struct {
	ID     string           `json:"id,omitempty"`
	Result types.ToolResult `json:"result"`
}

func (ToolCallResult) IsEvent() {}

func (ToolCallResult) wireMessageType() WireMessageType {
	return WireMessageTypeToolCallResult
}

// QuestionRequest asks a structured question that expects user answers.
type QuestionRequest struct {
	ID            string         `json:"id"`
	Prompt        string         `json:"prompt"`
	Items         []QuestionItem `json:"items,omitempty"`
	AllowMultiple bool           `json:"allow_multiple,omitempty"`
}

func (QuestionRequest) IsRequest() {}

func (QuestionRequest) wireMessageType() WireMessageType {
	return WireMessageTypeQuestionRequest
}

// QuestionResponse carries answers for a prior question request.
type QuestionResponse struct {
	RequestID   string            `json:"request_id"`
	Answers     map[string]string `json:"answers,omitempty"`
	SubmittedAt string            `json:"submitted_at,omitempty"`
}

func (QuestionResponse) IsRequest() {}

func (QuestionResponse) wireMessageType() WireMessageType {
	return WireMessageTypeQuestionResponse
}

// QuestionOption is one selectable option in a question item.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value"`
}

func (QuestionOption) IsRequest() {}

func (QuestionOption) wireMessageType() WireMessageType {
	return WireMessageTypeQuestionOption
}

// QuestionItem is one asked item in a structured question flow.
type QuestionItem struct {
	Header   string           `json:"header,omitempty"`
	ID       string           `json:"id"`
	Question string           `json:"question"`
	Options  []QuestionOption `json:"options,omitempty"`
}

func (QuestionItem) IsRequest() {}

func (QuestionItem) wireMessageType() WireMessageType {
	return WireMessageTypeQuestionItem
}
