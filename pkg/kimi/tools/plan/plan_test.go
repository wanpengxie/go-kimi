package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestPlanStateLifecycle(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	planFile := filepath.Join(workDir, "plan.md")
	const content = "phase 1\nphase 2\n"
	if err := os.WriteFile(planFile, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", planFile, err)
	}

	state := NewPlanState()
	if state.IsActive() {
		t.Fatal("state.IsActive() = true, want false")
	}
	if err := state.Enter(planFile); err != nil {
		t.Fatalf("Enter() error = %v", err)
	}
	if !state.IsActive() {
		t.Fatal("state.IsActive() = false, want true")
	}

	got, err := state.Exit()
	if err != nil {
		t.Fatalf("Exit() error = %v", err)
	}
	if got != content {
		t.Fatalf("Exit() content = %q, want %q", got, content)
	}
	if state.IsActive() {
		t.Fatal("state.IsActive() after Exit() = true, want false")
	}
	if state.PlanFile != "" {
		t.Fatalf("state.PlanFile after Exit() = %q, want empty", state.PlanFile)
	}
}

func TestPlanStateRejectsDuplicateEnter(t *testing.T) {
	t.Parallel()

	state := NewPlanState()
	if err := state.Enter("/tmp/plan-a.md"); err != nil {
		t.Fatalf("Enter() error = %v", err)
	}
	if err := state.Enter("/tmp/plan-b.md"); err == nil {
		t.Fatal("second Enter() error = nil, want duplicate enter error")
	}
}

func TestPlanStateExitWithoutEnter(t *testing.T) {
	t.Parallel()

	state := NewPlanState()
	if _, err := state.Exit(); err == nil {
		t.Fatal("Exit() error = nil, want not-active error")
	}
}

func TestEnterPlanModeExecuteSetsPathAndActivates(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	state := NewPlanState()
	tool := NewEnterPlanMode(workDir, state)
	tool.slugGenerator = func() (string, error) {
		return "calm-river-plan", nil
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if result.Name != enterToolName {
		t.Fatalf("result.Name = %q, want %q", result.Name, enterToolName)
	}

	payload := resultPayloadMap(t, result)
	planFile, _ := payload["plan_file"].(string)
	wantPlanFile := filepath.Join(workDir, ".kimi", "plans", "calm-river-plan.md")
	if planFile != wantPlanFile {
		t.Fatalf("plan_file = %q, want %q", planFile, wantPlanFile)
	}
	active, ok := payload["active"].(bool)
	if !ok || !active {
		t.Fatalf("payload.active = %#v, want true", payload["active"])
	}
	if !state.IsActive() {
		t.Fatal("state.IsActive() = false, want true")
	}
	if state.PlanFile != wantPlanFile {
		t.Fatalf("state.PlanFile = %q, want %q", state.PlanFile, wantPlanFile)
	}
}

func TestEnterPlanModeExecuteRejectsDuplicateEnter(t *testing.T) {
	t.Parallel()

	tool := NewEnterPlanMode(t.TempDir(), NewPlanState())
	tool.slugGenerator = func() (string, error) {
		return "steady-harbor-notes", nil
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("second Execute() error = nil, want duplicate enter error")
	}
}

func TestEnterPlanModeExecuteRejectsUnexpectedParams(t *testing.T) {
	t.Parallel()

	tool := NewEnterPlanMode(t.TempDir(), NewPlanState())
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"unexpected": true,
	})); err == nil {
		t.Fatal("Execute(unexpected params) error = nil, want validation error")
	}
}

func TestExitPlanModeExecuteReturnsPlanContentAndDecision(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	planFile := filepath.Join(workDir, "plan.md")
	const content = "# Plan\n- implement\n- test\n"
	if err := os.WriteFile(planFile, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", planFile, err)
	}

	state := NewPlanState()
	if err := state.Enter(planFile); err != nil {
		t.Fatalf("state.Enter() error = %v", err)
	}
	tool := NewExitPlanMode(state)

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"decision": "approve",
		"feedback": "looks good",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if result.Name != exitToolName {
		t.Fatalf("result.Name = %q, want %q", result.Name, exitToolName)
	}

	payload := resultPayloadMap(t, result)
	if got, _ := payload["decision"].(string); got != "approve" {
		t.Fatalf("payload.decision = %q, want %q", got, "approve")
	}
	if got, _ := payload["feedback"].(string); got != "looks good" {
		t.Fatalf("payload.feedback = %q, want %q", got, "looks good")
	}
	if got, _ := payload["plan_content"].(string); got != content {
		t.Fatalf("payload.plan_content = %q, want %q", got, content)
	}
	if active, _ := payload["active"].(bool); active {
		t.Fatalf("payload.active = %v, want false", active)
	}
	if state.IsActive() {
		t.Fatal("state.IsActive() after exit = true, want false")
	}
}

func TestExitPlanModeExecuteRejectsInvalidDecision(t *testing.T) {
	t.Parallel()

	tool := NewExitPlanMode(NewPlanState())
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"decision": "skip",
	})); err == nil {
		t.Fatal("Execute(invalid decision) error = nil, want validation error")
	}
}

func TestExitPlanModeExecuteWithoutActivePlan(t *testing.T) {
	t.Parallel()

	tool := NewExitPlanMode(NewPlanState())
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"decision": "approve",
	})); err == nil {
		t.Fatal("Execute() error = nil, want not-active error")
	}
}

func TestExitPlanModeExecuteReadFailureKeepsPlanActive(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	missingPlan := filepath.Join(workDir, "missing-plan.md")
	state := NewPlanState()
	if err := state.Enter(missingPlan); err != nil {
		t.Fatalf("state.Enter() error = %v", err)
	}

	tool := NewExitPlanMode(state)
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"decision": "revise",
	})); err == nil {
		t.Fatal("Execute(read missing file) error = nil, want read error")
	}
	if !state.IsActive() {
		t.Fatal("state.IsActive() = false, want true after failed exit")
	}
}

func TestExitPlanModeExecuteTruncatesLongPlanContent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	planFile := filepath.Join(workDir, "plan.md")
	longContent := strings.Repeat("x", tools.MaxOutputChars+300)
	if err := os.WriteFile(planFile, []byte(longContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", planFile, err)
	}

	state := NewPlanState()
	if err := state.Enter(planFile); err != nil {
		t.Fatalf("state.Enter() error = %v", err)
	}
	tool := NewExitPlanMode(state)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"decision": "approve",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := resultPayloadMap(t, result)
	content, _ := payload["plan_content"].(string)
	if utf8.RuneCountInString(content) > tools.MaxOutputChars {
		t.Fatalf("plan_content rune count = %d, want <= %d", utf8.RuneCountInString(content), tools.MaxOutputChars)
	}
	if !strings.Contains(content, "...[truncated]") && !strings.Contains(content, "...[line-truncated]") {
		t.Fatalf("plan_content = %q, want line/output truncation suffix", content)
	}
}

func mustParams(t *testing.T, input any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func resultPayloadMap(t *testing.T, result types.ToolResult) map[string]any {
	t.Helper()
	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result.Value.Value type = %T, want map[string]any", result.Value.Value)
	}
	return payload
}
