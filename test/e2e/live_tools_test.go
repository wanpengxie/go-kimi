//go:build e2e_live

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm/moonshot"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	toolfile "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/file"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/shell"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/think"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveSoulWithThinkTool(t *testing.T) {
	live := newLiveRuntime(t, tools.NewMapToolRegistry(think.New()), "")

	result, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "Before answering, call the think tool exactly once with a short thought, then reply with EXACT text THINK_TOOL_DONE."},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !strings.Contains(output, "THINK_TOOL_DONE") {
		t.Fatalf("live response = %q, want contains THINK_TOOL_DONE", output)
	}
	if !contextHasToolCallName(live.ctxStore.Messages(), "think") {
		t.Fatalf("context does not contain think tool call: %#v", live.ctxStore.Messages())
	}
}

func TestLiveSoulWithShellTool(t *testing.T) {
	workDir := t.TempDir()
	live := newLiveRuntime(t, tools.NewMapToolRegistry(shell.New(workDir, nil)), "")

	const token = "LIVE_SHELL_TOKEN_OK_2026"
	result, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "Use the shell tool to run EXACT command: printf " + token + ". Then respond with the command output only."},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !strings.Contains(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
	if !contextHasToolCallName(live.ctxStore.Messages(), "shell") {
		t.Fatalf("context does not contain shell tool call: %#v", live.ctxStore.Messages())
	}
	if !contextHasToolOutputContains(live.ctxStore.Messages(), token) {
		t.Fatalf("tool output does not contain %q, messages=%#v", token, live.ctxStore.Messages())
	}
}

func TestLiveSoulWithReadFile(t *testing.T) {
	workDir := t.TempDir()
	const token = "LIVE_READ_FILE_TOKEN_2026"
	targetPath := filepath.Join(workDir, "memo.txt")
	if err := os.WriteFile(targetPath, []byte("memo:"+token+"\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
	}

	live := newLiveRuntime(t, tools.NewMapToolRegistry(toolfile.NewReadFile(workDir)), "")
	result, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "Use read_file tool on path memo.txt and reply with the token in that file only."},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !strings.Contains(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
	if !contextHasToolCallName(live.ctxStore.Messages(), "read_file") {
		t.Fatalf("context does not contain read_file tool call: %#v", live.ctxStore.Messages())
	}
}

func TestLiveSoulWithMultipleTurns(t *testing.T) {
	live := newLiveRuntime(t, nil, "")
	const token = "LIVE_MULTI_TURN_TOKEN_2026"

	firstResult, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "Remember this token for later: " + token + ". Reply with STORED."},
	})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	firstOutput := strings.TrimSpace(liveTextFromContentParts(firstResult.Content))
	if firstOutput == "" {
		t.Fatalf("first live response is empty: %#v", firstResult.Content)
	}

	secondResult, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "What is the token I asked you to remember? Reply with token only."},
	})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	secondOutput := strings.TrimSpace(liveTextFromContentParts(secondResult.Content))
	if !strings.Contains(strings.ToUpper(secondOutput), token) {
		t.Fatalf("second live response = %q, want contains %q", secondOutput, token)
	}

	messages := live.ctxStore.Messages()
	if len(messages) < 4 {
		t.Fatalf("context message count = %d, want >= 4 for two turns", len(messages))
	}
}

func TestLiveSoulWithSystemPrompt(t *testing.T) {
	const prefix = "SYSTEM_PROMPT_OK:"
	systemPrompt := "Every response must start with " + prefix

	live := newLiveRuntime(t, nil, systemPrompt)
	result, err := live.engine.Run(live.ctx, types.ContentParts{
		types.TextPart{Text: "Reply with a short greeting."},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !strings.HasPrefix(output, prefix) {
		t.Fatalf("live response = %q, want prefix %q", output, prefix)
	}
}

type liveRuntime struct {
	ctx      context.Context
	engine   *soul.Soul
	ctxStore *soul.SoulContext
}

func newLiveRuntime(t *testing.T, registry soul.ToolRegistry, systemPrompt string) liveRuntime {
	t.Helper()

	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Fatal(
			"KIMI_API_KEY is required for e2e_live (real HTTP). " +
				"Optional: KIMI_BASE_URL (defaults to Moonshot API base), " +
				"KIMI_MODEL (when empty, resolve from /models).",
		)
	}

	baseURL := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	resolvedModel, err := resolveLiveModel(ctx, apiKey, baseURL, os.Getenv("KIMI_MODEL"))
	if err != nil {
		t.Fatalf("resolve live model error = %v", err)
	}
	t.Logf("live model resolved from %s: %s", resolvedModel.Source, resolvedModel.Model)

	provider := moonshot.NewMoonshotClient(apiKey, baseURL, resolvedModel.Model)
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, registry, wire.NoopEmitter{}, strings.TrimSpace(systemPrompt))
	return liveRuntime{
		ctx:      ctx,
		engine:   engine,
		ctxStore: ctxStore,
	}
}

func contextHasToolCallName(messages []soul.Message, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	for i := range messages {
		for j := range messages[i].ToolCalls {
			if strings.TrimSpace(messages[i].ToolCalls[j].Name) == toolName {
				return true
			}
		}
	}
	return false
}

func contextHasToolOutputContains(messages []soul.Message, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for i := range messages {
		if messages[i].Role != soul.RoleTool {
			continue
		}
		output := liveTextFromContentParts(messages[i].Content)
		if strings.Contains(output, needle) {
			return true
		}
	}
	return false
}
