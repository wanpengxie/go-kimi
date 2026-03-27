package subagents

import (
	"encoding/json"
	"testing"
)

func TestAgentInstanceRecordJSONRoundTrip(t *testing.T) {
	t.Parallel()

	record := AgentInstanceRecord{
		AgentID:      "agent-001",
		SubagentType: "researcher",
		Status:       StatusRunningForeground,
		Description:  "Investigate root cause",
		CreatedAt:    1710000000.1,
		UpdatedAt:    1710000100.2,
		LastTaskID:   "task-123",
		LaunchSpec: AgentLaunchSpec{
			AgentID:        "agent-001",
			SubagentType:   "researcher",
			ModelOverride:  "kimi-k2.5",
			EffectiveModel: "kimi-k2.5",
			CreatedAt:      1710000000.1,
		},
	}

	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(record) error = %v", err)
	}

	var decoded AgentInstanceRecord
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(record) error = %v", err)
	}

	if decoded.AgentID != record.AgentID {
		t.Fatalf("decoded.AgentID = %q, want %q", decoded.AgentID, record.AgentID)
	}
	if decoded.SubagentType != record.SubagentType {
		t.Fatalf("decoded.SubagentType = %q, want %q", decoded.SubagentType, record.SubagentType)
	}
	if decoded.Status != record.Status {
		t.Fatalf("decoded.Status = %q, want %q", decoded.Status, record.Status)
	}
	if decoded.LastTaskID != record.LastTaskID {
		t.Fatalf("decoded.LastTaskID = %q, want %q", decoded.LastTaskID, record.LastTaskID)
	}
	if decoded.LaunchSpec.ModelOverride != record.LaunchSpec.ModelOverride {
		t.Fatalf("decoded.LaunchSpec.ModelOverride = %q, want %q", decoded.LaunchSpec.ModelOverride, record.LaunchSpec.ModelOverride)
	}
	if decoded.LaunchSpec.EffectiveModel != record.LaunchSpec.EffectiveModel {
		t.Fatalf("decoded.LaunchSpec.EffectiveModel = %q, want %q", decoded.LaunchSpec.EffectiveModel, record.LaunchSpec.EffectiveModel)
	}
}

func TestAgentTypeDefinitionJSONRoundTrip(t *testing.T) {
	t.Parallel()

	definition := AgentTypeDefinition{
		Name:         "planner",
		Description:  "Plans work items",
		WhenToUse:    "Use for decomposition",
		DefaultModel: "kimi-k2",
		ToolPolicy: ToolPolicy{
			Mode:      ToolPolicyAllowlist,
			Allowlist: []string{"shell", "think"},
		},
		SupportsBackground: true,
	}

	payload, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("json.Marshal(definition) error = %v", err)
	}

	var decoded AgentTypeDefinition
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(definition) error = %v", err)
	}

	if decoded.Name != definition.Name {
		t.Fatalf("decoded.Name = %q, want %q", decoded.Name, definition.Name)
	}
	if decoded.ToolPolicy.Mode != ToolPolicyAllowlist {
		t.Fatalf("decoded.ToolPolicy.Mode = %q, want %q", decoded.ToolPolicy.Mode, ToolPolicyAllowlist)
	}
	if len(decoded.ToolPolicy.Allowlist) != 2 {
		t.Fatalf("len(decoded.ToolPolicy.Allowlist) = %d, want 2", len(decoded.ToolPolicy.Allowlist))
	}
	if !decoded.SupportsBackground {
		t.Fatal("decoded.SupportsBackground = false, want true")
	}
}
