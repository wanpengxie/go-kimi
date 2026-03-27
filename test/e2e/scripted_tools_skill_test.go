//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/skill"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	toolfile "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/file"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/shell"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/think"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestScriptedThinkTool(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-think-1",
						Name: "think",
						Arguments: map[string]any{
							"thought": "outline the response",
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "think handled"}},
				{Done: true},
			},
		},
	}

	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(think.New()),
		wire.NoopEmitter{},
		"",
	)

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "use think before answering"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "think handled" {
		t.Fatalf("result text = %q, want %q", got, "think handled")
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	if len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].Name != "think" {
		t.Fatalf("assistant tool calls = %#v, want one think call", messages[1].ToolCalls)
	}
	if messages[2].Role != soul.RoleTool {
		t.Fatalf("messages[2].Role = %q, want tool", messages[2].Role)
	}
	if got := strings.TrimSpace(textFromContentParts(messages[2].Content)); got != "" {
		t.Fatalf("think tool output = %q, want empty string", got)
	}
}

func TestScriptedShellTool(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-shell-1",
						Name: "shell",
						Arguments: map[string]any{
							"command": "printf hello-scripted-shell",
							"timeout": 5,
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "shell handled"}},
				{Done: true},
			},
		},
	}

	wireCh := make(chan wire.WireMessage, 32)
	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(shell.New(workDir, nil)),
		wire.ChannelEmitter{Ch: wireCh},
		"",
	)

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "run shell command"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "shell handled" {
		t.Fatalf("result text = %q, want %q", got, "shell handled")
	}

	events := drainWireMessages(wireCh)
	toolResult, ok := findToolResultByID(events, "call-shell-1")
	if !ok {
		t.Fatalf("tool result event for call-shell-1 not found, events=%#v", events)
	}
	if toolResult.IsError {
		t.Fatalf("shell tool result IsError = true, want false (result=%#v)", toolResult)
	}
	output := fmt.Sprintf("%v", toolResult.Value.Value)
	if !strings.Contains(output, "hello-scripted-shell") {
		t.Fatalf("shell tool output = %q, want contains hello-scripted-shell", output)
	}
}

func TestScriptedReadFileTool(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	targetPath := filepath.Join(workDir, "note.txt")
	content := "line-one\nline-two\nline-three\n"
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
	}

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-read-1",
						Name: "read_file",
						Arguments: map[string]any{
							"path":        "note.txt",
							"line_offset": 2,
							"n_lines":     2,
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "read handled"}},
				{Done: true},
			},
		},
	}

	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(toolfile.NewReadFile(workDir)),
		wire.NoopEmitter{},
		"",
	)

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "read file now"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "read handled" {
		t.Fatalf("result text = %q, want %q", got, "read handled")
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	toolOutput := strings.TrimSpace(textFromContentParts(messages[2].Content))
	if toolOutput != "line-two\nline-three" {
		t.Fatalf("tool output = %q, want %q", toolOutput, "line-two\\nline-three")
	}
}

func TestScriptedApprovalWithShell(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		t.Parallel()
		runScriptedShellApprovalCase(t, soul.ApprovalApprove, false)
	})

	t.Run("reject", func(t *testing.T) {
		t.Parallel()
		runScriptedShellApprovalCase(t, soul.ApprovalReject, true)
	})
}

func TestScriptedSkillDiscovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, "summarize")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", skillDir, err)
	}

	skillFile := filepath.Join(skillDir, skill.FileName)
	markdown := `---
name: summarize
description: summarize text into bullet points
type: standard
---
Summarize the provided text into concise bullets.
`
	if err := os.WriteFile(skillFile, []byte(markdown), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", skillFile, err)
	}

	discovered, err := skill.DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %d, want 1", len(discovered))
	}
	got := discovered[0]
	if got.Name != "summarize" {
		t.Fatalf("skill.Name = %q, want summarize", got.Name)
	}
	if got.Description != "summarize text into bullet points" {
		t.Fatalf("skill.Description = %q, want summarize text into bullet points", got.Description)
	}
	if got.Type != "standard" {
		t.Fatalf("skill.Type = %q, want standard", got.Type)
	}
	if strings.TrimSpace(got.Content) != "Summarize the provided text into concise bullets." {
		t.Fatalf("skill.Content = %q, want parsed body", got.Content)
	}
}

func runScriptedShellApprovalCase(t *testing.T, decision soul.ApprovalDecision, expectRejected bool) {
	t.Helper()

	workDir := t.TempDir()
	targetFile := filepath.Join(workDir, "approval-shell.txt")
	command := "printf scripted-approval > " + shellQuotePath(targetFile)

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-shell-approval",
						Name: "shell",
						Arguments: map[string]any{
							"command": command,
							"timeout": 5,
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "approval finished"}},
				{Done: true},
			},
		},
	}

	wireCh := make(chan wire.WireMessage, 32)
	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(shell.New(workDir, nil)),
		wire.ChannelEmitter{Ch: wireCh},
		"",
	)
	engine.SetYolo(false)

	outcomeCh := make(chan runOutcome, 1)
	go func() {
		result, err := engine.Run(context.Background(), types.ContentParts{
			types.TextPart{Text: "execute shell with approval"},
		})
		outcomeCh <- runOutcome{result: result, err: err}
	}()

	request, events := waitForApprovalRequest(t, wireCh, 2*time.Second)
	feedback := ""
	if decision == soul.ApprovalReject {
		feedback = "blocked by scripted approval test"
	}
	if err := engine.RespondApproval(request.ID, decision, feedback); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}

	outcome := waitRunOutcome(t, outcomeCh, 2*time.Second)
	if outcome.err != nil {
		t.Fatalf("Run() error = %v", outcome.err)
	}
	if got := strings.TrimSpace(textFromContentParts(outcome.result.Content)); got != "approval finished" {
		t.Fatalf("result text = %q, want %q", got, "approval finished")
	}

	if expectRejected {
		if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
			t.Fatalf("target file should not exist after reject, stat err = %v", err)
		}
		messages := ctxStore.Messages()
		if len(messages) != 4 {
			t.Fatalf("context message count = %d, want 4", len(messages))
		}
		toolMessage := strings.TrimSpace(textFromContentParts(messages[2].Content))
		if !strings.Contains(toolMessage, "rejected") {
			t.Fatalf("tool rejection message = %q, want contains rejected", toolMessage)
		}
	} else {
		data, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v", targetFile, err)
		}
		if string(data) != "scripted-approval" {
			t.Fatalf("written file content = %q, want %q", string(data), "scripted-approval")
		}
	}

	events = append(events, drainWireMessages(wireCh)...)
	if !hasApprovalRequest(events) {
		t.Fatalf("approval request event missing, events=%#v", events)
	}
}

func findToolResultByID(events []wire.WireMessage, callID string) (types.ToolResult, bool) {
	for i := range events {
		resultEvent, ok := events[i].(wire.ToolCallResult)
		if !ok {
			continue
		}
		if resultEvent.Result.ToolCallID == callID {
			return resultEvent.Result, true
		}
	}
	return types.ToolResult{}, false
}

func shellQuotePath(path string) string {
	path = strings.ReplaceAll(path, `'`, `'"'"'`)
	return "'" + path + "'"
}
