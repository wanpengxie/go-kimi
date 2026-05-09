//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/internal/soul"
	kimi "github.com/wanpengxie/go-kimi/pkg/kimi"
	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/session"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestIntegrationAgentSessionResume(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	const firstToken = "INTEGRATION_SESSION_FIRST_TOKEN_2026"
	const secondToken = "INTEGRATION_SESSION_SECOND_TOKEN_2026"

	provider1 := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: firstToken}},
			{Done: true},
		}},
	}
	agent1, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  workDir,
		Config:   m8ScriptedConfig(),
		Provider: provider1,
	})
	if err != nil {
		t.Fatalf("NewAgent(first) error = %v", err)
	}
	if err := agent1.Run(context.Background(), "first turn"); err != nil {
		t.Fatalf("agent1.Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent1.LastResult().Content)); got != firstToken {
		t.Fatalf("first LastResult text = %q, want %q", got, firstToken)
	}
	if err := agent1.Close(); err != nil {
		t.Fatalf("agent1.Close() error = %v", err)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	contextPayload, err := os.ReadFile(continued.ContextFile)
	if err != nil {
		t.Fatalf("os.ReadFile(context) error = %v", err)
	}
	if strings.TrimSpace(string(contextPayload)) == "" {
		t.Fatal("context.jsonl should not be empty after first turn")
	}
	wirePayload, err := os.ReadFile(continued.WireFile)
	if err != nil {
		t.Fatalf("os.ReadFile(wire) error = %v", err)
	}
	if strings.TrimSpace(string(wirePayload)) == "" {
		t.Fatal("wire.jsonl should not be empty after first turn")
	}

	provider2 := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: secondToken}},
			{Done: true},
		}},
	}
	agent2, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:   workDir,
		SessionID: continued.ID,
		Config:    m8ScriptedConfig(),
		Provider:  provider2,
	})
	if err != nil {
		t.Fatalf("NewAgent(second) error = %v", err)
	}
	if err := agent2.Run(context.Background(), "second turn"); err != nil {
		t.Fatalf("agent2.Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent2.LastResult().Content)); got != secondToken {
		t.Fatalf("second LastResult text = %q, want %q", got, secondToken)
	}
	if err := agent2.Close(); err != nil {
		t.Fatalf("agent2.Close() error = %v", err)
	}

	requests := provider2.Requests()
	if len(requests) != 1 {
		t.Fatalf("second provider request count = %d, want 1", len(requests))
	}
	if !m8MessagesContainText(requests[0].Messages, firstToken) {
		t.Fatalf("second provider request missing prior context token %q, messages=%#v", firstToken, requests[0].Messages)
	}

	messages := integrationRestoreMessages(t, continued.Dir)
	if len(messages) < 4 {
		t.Fatalf("restored message count = %d, want >= 4", len(messages))
	}
	if integrationCountRole(messages, soul.RoleUser) < 2 {
		t.Fatalf("restored user message count = %d, want >= 2", integrationCountRole(messages, soul.RoleUser))
	}
	if integrationCountRole(messages, soul.RoleAssistant) < 2 {
		t.Fatalf("restored assistant message count = %d, want >= 2", integrationCountRole(messages, soul.RoleAssistant))
	}
}

func TestIntegrationAgentApprovalFlowWithPersistence(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	runtime := approvalruntime.NewApprovalRuntime()
	events := runtime.Subscribe()
	defer runtime.Unsubscribe(events)

	var calls atomic.Int32
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "integration-approval-call", Name: "m8_echo", Arguments: map[string]any{"message": "integration-approval"}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "INTEGRATION_APPROVAL_DONE_2026"}},
				{Done: true},
			},
		},
	}

	forceYoloOff := false
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:         workDir,
		Config:          m8ScriptedConfig(),
		Provider:        provider,
		AdditionalTools: []tools.Tool{&m8EchoTool{calls: &calls}},
		ApprovalRuntime: runtime,
		Overrides: kimi.AgentOverrides{
			DefaultYolo: &forceYoloOff,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	outcome := make(chan error, 1)
	go func() {
		outcome <- agent.Run(context.Background(), "run approval integration flow")
	}()

	created := m8WaitApprovalEvent(t, events, approvalruntime.EventRequestCreated, 2*time.Second)
	if created.Record == nil || strings.TrimSpace(created.Record.ID) == "" {
		t.Fatalf("created approval event record = %#v, want non-empty id", created.Record)
	}
	if err := runtime.Resolve(created.Record.ID, approvalruntime.ApprovalApprove, ""); err != nil {
		t.Fatalf("runtime.Resolve() error = %v", err)
	}
	resolved := m8WaitApprovalEvent(t, events, approvalruntime.EventRequestResolved, 2*time.Second)
	if resolved.Record == nil || resolved.Record.Decision == nil {
		t.Fatalf("resolved approval event record = %#v, want resolved decision", resolved.Record)
	}
	if *resolved.Record.Decision != approvalruntime.ApprovalApprove {
		t.Fatalf("resolved decision = %v, want approve", *resolved.Record.Decision)
	}

	if err := <-outcome; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("m8_echo execute count = %d, want 1", got)
	}
	if got := strings.TrimSpace(textFromContentParts(agent.LastResult().Content)); got != "INTEGRATION_APPROVAL_DONE_2026" {
		t.Fatalf("LastResult text = %q, want INTEGRATION_APPROVAL_DONE_2026", got)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if !m8MessagesContainText(requests[1].Messages, "m8-echo:integration-approval") {
		t.Fatalf("second request missing tool output m8-echo:integration-approval, messages=%#v", requests[1].Messages)
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	messages := integrationRestoreMessages(t, continued.Dir)
	if integrationCountRole(messages, soul.RoleTool) < 1 {
		t.Fatalf("restored tool message count = %d, want >= 1", integrationCountRole(messages, soul.RoleTool))
	}
	if !integrationHasToolOutput(messages, "m8-echo:integration-approval") {
		t.Fatalf("restored context missing tool output m8-echo:integration-approval, messages=%#v", messages)
	}
}

func TestIntegrationPlanModeLifecycleAcrossTurns(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	const enterToken = "INTEGRATION_PLAN_MODE_ENTERED_2026"
	const exitToken = "INTEGRATION_PLAN_MODE_EXITED_2026"

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "integration-plan-enter", Name: "enter_plan_mode", Arguments: map[string]any{}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: enterToken}},
				{Done: true},
			},
			{
				{ToolCall: &types.ToolCall{ID: "integration-plan-exit", Name: "exit_plan_mode", Arguments: map[string]any{"decision": "approve"}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: exitToken}},
				{Done: true},
			},
		},
	}

	forceYoloOn := true
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  workDir,
		Config:   m8ScriptedConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			DefaultYolo: &forceYoloOn,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	if err := agent.Run(context.Background(), "enter plan mode first"); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent.LastResult().Content)); got != enterToken {
		t.Fatalf("first LastResult text = %q, want %q", got, enterToken)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() after enter error = %v", err)
	}
	if continued.State == nil || !continued.State.PlanMode {
		t.Fatalf("session state after enter = %#v, want plan mode active", continued.State)
	}
	slug := strings.TrimSpace(continued.State.PlanSlug)
	if slug == "" {
		t.Fatalf("session PlanSlug = %q, want non-empty", slug)
	}

	planPath := filepath.Join(workDir, ".kimi", "plans", slug+".md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(plan dir) error = %v", err)
	}
	const planLine = "ship v1 integration"
	if err := os.WriteFile(planPath, []byte("# integration plan\n- "+planLine+"\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(plan) error = %v", err)
	}

	if err := agent.Run(context.Background(), "exit plan mode now"); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent.LastResult().Content)); got != exitToken {
		t.Fatalf("second LastResult text = %q, want %q", got, exitToken)
	}
	if provider.CallCount() != 4 {
		t.Fatalf("provider call count = %d, want 4", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 4 {
		t.Fatalf("provider request count = %d, want 4", len(requests))
	}
	if !m8MessagesContainText(requests[3].Messages, planLine) {
		t.Fatalf("final request missing plan content line %q, messages=%#v", planLine, requests[3].Messages)
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	persisted, err := session.Find(workDir, continued.ID)
	if err != nil {
		t.Fatalf("session.Find(%q) error = %v", continued.ID, err)
	}
	if persisted.State != nil && persisted.State.PlanMode {
		t.Fatalf("persisted session state after exit = %#v, want plan mode inactive", persisted.State)
	}
}

func TestIntegrationAgentShellToolRoundTrip(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	const token = "INTEGRATION_SHELL_TOOL_TOKEN_2026"

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "integration-shell-call", Name: "shell", Arguments: map[string]any{"command": "printf " + token}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: token}},
				{Done: true},
			},
		},
	}

	forceYoloOn := true
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  workDir,
		Config:   m8ScriptedConfig(),
		Provider: provider,
		Overrides: kimi.AgentOverrides{
			DefaultYolo: &forceYoloOn,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	if err := agent.Run(context.Background(), "run shell tool integration flow"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent.LastResult().Content)); got != token {
		t.Fatalf("LastResult text = %q, want %q", got, token)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if !m8MessagesContainText(requests[1].Messages, token) {
		t.Fatalf("second request missing shell token %q, messages=%#v", token, requests[1].Messages)
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	continued, err := session.Continue(workDir)
	if err != nil {
		t.Fatalf("session.Continue() error = %v", err)
	}
	messages := integrationRestoreMessages(t, continued.Dir)
	if integrationCountRole(messages, soul.RoleTool) < 1 {
		t.Fatalf("restored tool message count = %d, want >= 1", integrationCountRole(messages, soul.RoleTool))
	}
	if !integrationHasToolOutput(messages, token) {
		t.Fatalf("restored tool output missing token %q, messages=%#v", token, messages)
	}
}

func integrationRestoreMessages(t *testing.T, sessionDir string) []soul.Message {
	t.Helper()

	ctxStore := soul.NewSoulContext(sessionDir)
	if err := ctxStore.Restore(); err != nil {
		t.Fatalf("SoulContext.Restore(%q) error = %v", sessionDir, err)
	}
	return ctxStore.Messages()
}

func integrationCountRole(messages []soul.Message, role soul.Role) int {
	count := 0
	for i := range messages {
		if messages[i].Role == role {
			count++
		}
	}
	return count
}

func integrationHasToolOutput(messages []soul.Message, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for i := range messages {
		if messages[i].Role != soul.RoleTool {
			continue
		}
		if strings.Contains(textFromContentParts(messages[i].Content), needle) {
			return true
		}
	}
	return false
}
