package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestToolExecuteForeground(t *testing.T) {
	t.Parallel()

	runner := &fakeForegroundRunner{
		output: types.ToolReturnValue{
			Value: map[string]any{
				"agent_id":    "agent-1",
				"output_text": "done",
			},
		},
	}
	tool := New(runner, nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"  plan task  "}`))
	if err != nil {
		t.Fatalf("Execute(foreground) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(foreground) IsError = true, result=%#v", result)
	}
	if result.Name != toolName {
		t.Fatalf("result.Name = %q, want %q", result.Name, toolName)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("runner call count = %d, want 1", len(runner.calls))
	}
	if got := runner.calls[0].Prompt; got != "plan task" {
		t.Fatalf("runner prompt = %q, want %q", got, "plan task")
	}
	if got := runner.calls[0].SubagentType; got != defaultSubagentType {
		t.Fatalf("runner subagent_type = %q, want %q", got, defaultSubagentType)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["run_in_background"].(bool); got {
		t.Fatalf("result run_in_background = %v, want false", got)
	}
	if got, _ := payload["output_text"].(string); got != "done" {
		t.Fatalf("result output_text = %q, want %q", got, "done")
	}
}

func TestToolExecuteForegroundResumeWithModelOverride(t *testing.T) {
	t.Parallel()

	runner := &fakeForegroundRunner{
		output: types.ToolReturnValue{
			Value: map[string]any{
				"agent_id":    "agent-resume",
				"output_text": "continued",
			},
		},
	}
	tool := New(runner, nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
	  "agent_id":" agent-resume ",
	  "prompt":"continue with updated context",
	  "model_override":" kimi-k2.5 "
	}`))
	if err != nil {
		t.Fatalf("Execute(foreground resume) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(foreground resume) IsError = true, result=%#v", result)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner call count = %d, want 1", len(runner.calls))
	}
	if got := runner.calls[0].AgentID; got != "agent-resume" {
		t.Fatalf("runner agent_id = %q, want %q", got, "agent-resume")
	}
	if got := runner.calls[0].ModelOverride; got != "kimi-k2.5" {
		t.Fatalf("runner model_override = %q, want %q", got, "kimi-k2.5")
	}
}

func TestToolExecuteForegroundRunnerError(t *testing.T) {
	t.Parallel()

	tool := New(&fakeForegroundRunner{err: errors.New("runner failed")}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"do work"}`))
	if err != nil {
		t.Fatalf("Execute(foreground runner error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(foreground runner error) IsError = false, want true")
	}
	text, _ := result.Value.Value.(string)
	if !strings.Contains(text, "runner failed") {
		t.Fatalf("error text = %q, want contains runner failed", text)
	}
}

func TestToolExecuteForegroundMissingRunner(t *testing.T) {
	t.Parallel()

	tool := New(nil, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"do work"}`))
	if err != nil {
		t.Fatalf("Execute(missing runner) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(missing runner) IsError = false, want true")
	}
}

func TestToolExecuteBackground(t *testing.T) {
	t.Parallel()

	manager := &fakeBackgroundManager{taskID: "task-123"}
	tool := New(nil, manager)
	tool.SessionID = "session-42"
	tool.TimeoutSec = 12

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
	  "agent_id":"agent-existing",
	  "prompt":"delegate to planner",
	  "subagent_type":"planner",
	  "run_in_background":true,
	  "model_override":"kimi-k2.5"
	}`))
	if err != nil {
		t.Fatalf("Execute(background) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(background) IsError = true, result=%#v", result)
	}

	if len(manager.calls) != 1 {
		t.Fatalf("CreateAgentTask call count = %d, want 1", len(manager.calls))
	}
	spec := manager.calls[0]
	if spec.SessionID != "session-42" {
		t.Fatalf("spec.SessionID = %q, want %q", spec.SessionID, "session-42")
	}
	if spec.SubagentType != "planner" {
		t.Fatalf("spec.SubagentType = %q, want planner", spec.SubagentType)
	}
	if spec.AgentID != "agent-existing" {
		t.Fatalf("spec.AgentID = %q, want %q", spec.AgentID, "agent-existing")
	}
	if spec.Prompt != "delegate to planner" {
		t.Fatalf("spec.Prompt = %q, want %q", spec.Prompt, "delegate to planner")
	}
	if spec.ModelOverride != "kimi-k2.5" {
		t.Fatalf("spec.ModelOverride = %q, want %q", spec.ModelOverride, "kimi-k2.5")
	}
	if spec.TimeoutSec != 12 {
		t.Fatalf("spec.TimeoutSec = %d, want 12", spec.TimeoutSec)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["task_id"].(string); got != "task-123" {
		t.Fatalf("result task_id = %q, want task-123", got)
	}
	if got, _ := payload["run_in_background"].(bool); !got {
		t.Fatalf("result run_in_background = %v, want true", got)
	}
	if got, _ := payload["agent_id"].(string); got != "agent-existing" {
		t.Fatalf("result agent_id = %q, want %q", got, "agent-existing")
	}
	if got, _ := payload["model_override"].(string); got != "kimi-k2.5" {
		t.Fatalf("result model_override = %q, want %q", got, "kimi-k2.5")
	}
}

func TestToolExecuteBackgroundManagerError(t *testing.T) {
	t.Parallel()

	tool := New(nil, &fakeBackgroundManager{err: errors.New("create failed")})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
	  "prompt":"delegate",
	  "run_in_background":true
	}`))
	if err != nil {
		t.Fatalf("Execute(background manager error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(background manager error) IsError = false, want true")
	}
	text, _ := result.Value.Value.(string)
	if !strings.Contains(text, "create failed") {
		t.Fatalf("error text = %q, want contains create failed", text)
	}
}

func TestToolExecuteBackgroundMissingManager(t *testing.T) {
	t.Parallel()

	tool := New(nil, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
	  "prompt":"delegate",
	  "run_in_background":true
	}`))
	if err != nil {
		t.Fatalf("Execute(background missing manager) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(background missing manager) IsError = false, want true")
	}
}

func TestToolExecuteDecodeError(t *testing.T) {
	t.Parallel()

	tool := New(nil, nil)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":`)); err == nil {
		t.Fatal("Execute(invalid json) error = nil, want error")
	}
}

func TestToolExecuteMissingPrompt(t *testing.T) {
	t.Parallel()

	tool := New(nil, nil)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"subagent_type":"planner"}`)); err == nil {
		t.Fatal("Execute(missing prompt) error = nil, want error")
	}
}

type fakeForegroundRunner struct {
	calls  []subagents.ForegroundRunRequest
	output types.ToolReturnValue
	err    error
}

func (r *fakeForegroundRunner) Run(_ context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error) {
	r.calls = append(r.calls, req)
	if r.err != nil {
		return types.ToolReturnValue{}, r.err
	}
	return r.output, nil
}

type fakeBackgroundManager struct {
	calls  []corebg.TaskSpec
	taskID string
	err    error
}

func (m *fakeBackgroundManager) CreateAgentTask(_ context.Context, spec corebg.TaskSpec) (string, error) {
	m.calls = append(m.calls, spec)
	if m.err != nil {
		return "", m.err
	}
	if strings.TrimSpace(m.taskID) == "" {
		return "task-generated", nil
	}
	return m.taskID, nil
}
