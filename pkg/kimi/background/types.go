package background

// TaskStatus represents one background task lifecycle state.
type TaskStatus string

const (
	TaskCreated          TaskStatus = "created"
	TaskStarting         TaskStatus = "starting"
	TaskRunning          TaskStatus = "running"
	TaskAwaitingApproval TaskStatus = "awaiting_approval"
	TaskCompleted        TaskStatus = "completed"
	TaskFailed           TaskStatus = "failed"
	TaskKilled           TaskStatus = "killed"
)

// IsTerminal reports whether the status is terminal.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskCompleted, TaskFailed, TaskKilled:
		return true
	default:
		return false
	}
}

// TaskKind identifies how one background task executes.
type TaskKind string

const (
	TaskKindBash  TaskKind = "bash"
	TaskKindAgent TaskKind = "agent"
)

// TaskSpec stores immutable task configuration.
type TaskSpec struct {
	ID          string   `json:"id"`
	Kind        TaskKind `json:"kind"`
	SessionID   string   `json:"session_id"`
	Description string   `json:"description"`
	ToolCallID  string   `json:"tool_call_id"`

	// Bash-specific.
	Command    string `json:"command,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	TimeoutSec int    `json:"timeout_s,omitempty"`

	// Agent-specific.
	AgentID       string `json:"agent_id,omitempty"`
	SubagentType  string `json:"subagent_type,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	ModelOverride string `json:"model_override,omitempty"`
}

// TaskRuntime stores mutable runtime status.
type TaskRuntime struct {
	Status        TaskStatus `json:"status"`
	StartedAt     *float64   `json:"started_at,omitempty"`
	HeartbeatAt   *float64   `json:"heartbeat_at,omitempty"`
	FinishedAt    *float64   `json:"finished_at,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	TimedOut      bool       `json:"timed_out"`
	FailureReason string     `json:"failure_reason,omitempty"`
}

// TaskControl stores external control intents for one task.
type TaskControl struct {
	KillRequestedAt *float64 `json:"kill_requested_at,omitempty"`
	KillReason      string   `json:"kill_reason,omitempty"`
}

// TaskView aggregates task spec/runtime/control.
type TaskView struct {
	Spec    TaskSpec    `json:"spec"`
	Runtime TaskRuntime `json:"runtime"`
	Control TaskControl `json:"control"`
}
