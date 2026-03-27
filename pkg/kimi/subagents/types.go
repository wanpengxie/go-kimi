package subagents

// SubagentStatus tracks one subagent runtime lifecycle state.
type SubagentStatus string

const (
	StatusIdle              SubagentStatus = "idle"
	StatusRunningForeground SubagentStatus = "running_foreground"
	StatusRunningBackground SubagentStatus = "running_background"
	StatusCompleted         SubagentStatus = "completed"
	StatusFailed            SubagentStatus = "failed"
	StatusKilled            SubagentStatus = "killed"
)

// ToolPolicyMode controls how one subagent type can access tools.
type ToolPolicyMode string

const (
	ToolPolicyInherit   ToolPolicyMode = "inherit"
	ToolPolicyAllowlist ToolPolicyMode = "allowlist"
)

// ToolPolicy defines tool access restrictions for one subagent type.
type ToolPolicy struct {
	Mode      ToolPolicyMode `json:"mode"`
	Allowlist []string       `json:"allowlist,omitempty"`
}

// AgentTypeDefinition defines one subagent type template.
type AgentTypeDefinition struct {
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	WhenToUse          string     `json:"when_to_use"`
	DefaultModel       string     `json:"default_model,omitempty"`
	ToolPolicy         ToolPolicy `json:"tool_policy"`
	SupportsBackground bool       `json:"supports_background"`
}

// AgentLaunchSpec captures launch-time inputs/effective model for one instance.
type AgentLaunchSpec struct {
	AgentID        string  `json:"agent_id"`
	SubagentType   string  `json:"subagent_type"`
	ModelOverride  string  `json:"model_override,omitempty"`
	EffectiveModel string  `json:"effective_model,omitempty"`
	CreatedAt      float64 `json:"created_at"`
}

// AgentInstanceRecord stores persistent metadata for one subagent instance.
type AgentInstanceRecord struct {
	AgentID      string          `json:"agent_id"`
	SubagentType string          `json:"subagent_type"`
	Status       SubagentStatus  `json:"status"`
	Description  string          `json:"description"`
	CreatedAt    float64         `json:"created_at"`
	UpdatedAt    float64         `json:"updated_at"`
	LastTaskID   string          `json:"last_task_id,omitempty"`
	LaunchSpec   AgentLaunchSpec `json:"launch_spec"`
}
