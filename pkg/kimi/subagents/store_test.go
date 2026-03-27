package subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubagentStoreCreateGetUpdateListDelete(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "session", "subagents")
	store := NewSubagentStore(root)

	record := &AgentInstanceRecord{
		AgentID:      "agent-1",
		SubagentType: "planner",
		Status:       StatusIdle,
		Description:  "Plan next actions",
		CreatedAt:    1.1,
		UpdatedAt:    1.1,
		LaunchSpec: AgentLaunchSpec{
			CreatedAt: 1.1,
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create(agent-1) error = %v", err)
	}

	agentDir := filepath.Join(root, "agent-1")
	for _, file := range []string{metaFileName, contextFileName, wireFileName, promptFileName} {
		path := filepath.Join(agentDir, file)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("os.Stat(%q) error = %v", path, err)
		}
	}

	promptPath := filepath.Join(agentDir, promptFileName)
	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("os.ReadFile(prompt) error = %v", err)
	}
	if string(promptContent) != record.Description {
		t.Fatalf("prompt content = %q, want %q", string(promptContent), record.Description)
	}

	got, err := store.Get("agent-1")
	if err != nil {
		t.Fatalf("Get(agent-1) error = %v", err)
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("Get(agent-1).AgentID = %q, want agent-1", got.AgentID)
	}
	if got.LaunchSpec.AgentID != "agent-1" {
		t.Fatalf("Get(agent-1).LaunchSpec.AgentID = %q, want agent-1", got.LaunchSpec.AgentID)
	}
	if got.LaunchSpec.SubagentType != "planner" {
		t.Fatalf("Get(agent-1).LaunchSpec.SubagentType = %q, want planner", got.LaunchSpec.SubagentType)
	}

	got.Status = StatusRunningBackground
	got.LastTaskID = "task-9"
	got.UpdatedAt = 2.2
	if err := store.Update(got); err != nil {
		t.Fatalf("Update(agent-1) error = %v", err)
	}

	updated, err := store.Get("agent-1")
	if err != nil {
		t.Fatalf("Get(agent-1) after update error = %v", err)
	}
	if updated.Status != StatusRunningBackground {
		t.Fatalf("updated status = %q, want %q", updated.Status, StatusRunningBackground)
	}
	if updated.LastTaskID != "task-9" {
		t.Fatalf("updated LastTaskID = %q, want task-9", updated.LastTaskID)
	}

	second := &AgentInstanceRecord{
		AgentID:      "agent-2",
		SubagentType: "writer",
		Status:       StatusCompleted,
		Description:  "Write report",
		CreatedAt:    3.3,
		UpdatedAt:    3.4,
		LaunchSpec: AgentLaunchSpec{
			AgentID:      "agent-2",
			SubagentType: "writer",
			CreatedAt:    3.3,
		},
	}
	if err := store.Create(second); err != nil {
		t.Fatalf("Create(agent-2) error = %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(list))
	}
	if list[0].AgentID != "agent-1" || list[1].AgentID != "agent-2" {
		t.Fatalf("List() AgentIDs = [%q %q], want [agent-1 agent-2]", list[0].AgentID, list[1].AgentID)
	}

	if err := store.Delete("agent-1"); err != nil {
		t.Fatalf("Delete(agent-1) error = %v", err)
	}
	if _, err := store.Get("agent-1"); err == nil {
		t.Fatal("Get(agent-1) after delete error = nil, want not found")
	}
}

func TestSubagentStoreListOnMissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "missing", "subagents"))
	list, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(list))
	}
}

func TestSubagentStoreCreateDuplicateAndDeleteMissing(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))
	record := &AgentInstanceRecord{
		AgentID:      "agent-dup",
		SubagentType: "planner",
		Status:       StatusIdle,
		CreatedAt:    1,
		UpdatedAt:    1,
		LaunchSpec: AgentLaunchSpec{
			CreatedAt: 1,
		},
	}

	if err := store.Create(record); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	if err := store.Create(record); err == nil {
		t.Fatal("Create(duplicate) error = nil, want error")
	}
	if err := store.Delete("missing-agent"); err == nil {
		t.Fatal("Delete(missing-agent) error = nil, want error")
	}
}

func TestSubagentStoreValidationErrors(t *testing.T) {
	t.Parallel()

	store := NewSubagentStore(filepath.Join(t.TempDir(), "subagents"))

	if err := store.Create(&AgentInstanceRecord{AgentID: "../bad", SubagentType: "planner"}); err == nil {
		t.Fatal("Create(path traversal) error = nil, want error")
	}
	if err := store.Create(&AgentInstanceRecord{AgentID: "agent", SubagentType: ""}); err == nil {
		t.Fatal("Create(missing subagent_type) error = nil, want error")
	}

	if _, err := store.Get("  "); err == nil {
		t.Fatal("Get(empty id) error = nil, want error")
	}
	if err := store.Delete("a/b"); err == nil {
		t.Fatal("Delete(invalid id) error = nil, want error")
	}

	mismatch := &AgentInstanceRecord{
		AgentID:      "agent-x",
		SubagentType: "planner",
		LaunchSpec: AgentLaunchSpec{
			AgentID: "agent-y",
		},
	}
	if err := store.Create(mismatch); err == nil {
		t.Fatal("Create(mismatched launch_spec.agent_id) error = nil, want error")
	}
}

func TestSubagentStoreUpdateMissingAndCorruptedRecord(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "subagents")
	store := NewSubagentStore(root)

	missing := &AgentInstanceRecord{
		AgentID:      "agent-missing",
		SubagentType: "planner",
		LaunchSpec: AgentLaunchSpec{
			CreatedAt: 1,
		},
	}
	if err := store.Update(missing); err == nil {
		t.Fatal("Update(missing) error = nil, want error")
	}

	badDir := filepath.Join(root, "agent-bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", badDir, err)
	}
	metaPath := filepath.Join(badDir, metaFileName)
	if err := os.WriteFile(metaPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", metaPath, err)
	}

	_, err := store.List()
	if err == nil {
		t.Fatal("List() on corrupted record error = nil, want error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("List() error = %v, want decode context", err)
	}
}

func TestSubagentStoreNilAndEmptyDir(t *testing.T) {
	t.Parallel()

	var nilStore *SubagentStore
	if err := nilStore.Create(&AgentInstanceRecord{AgentID: "a", SubagentType: "b"}); err == nil {
		t.Fatal("nil store Create() error = nil, want error")
	}

	emptyStore := NewSubagentStore("   ")
	_, err := emptyStore.List()
	if err == nil {
		t.Fatal("empty dir List() error = nil, want error")
	}
}
