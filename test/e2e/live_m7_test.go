//go:build e2e_live

package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/mcp"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	liveMCPCommandEnv = "MCP_E2E_STDIO_COMMAND"
	liveMCPArgsEnv    = "MCP_E2E_STDIO_ARGS"
)

func TestLiveMCPStdioServer(t *testing.T) {
	command, args := liveMCPServerCommand(t)

	loader := mcp.NewMCPToolLoader([]mcp.MCPServerConfig{
		{
			Name:      "live-mcp",
			Transport: mcp.TransportStdio,
			Command:   command,
			Args:      args,
		},
	})
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Fatalf("loader.Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	discoveredTools, err := loader.LoadAll(ctx)
	if err != nil {
		t.Skipf("live MCP stdio server unavailable (%s %v): %v", command, args, err)
	}
	if len(discoveredTools) == 0 {
		t.Fatalf("len(discoveredTools) = 0, want >= 1")
	}

	for i := range discoveredTools {
		if strings.TrimSpace(discoveredTools[i].Name()) == "" {
			t.Fatalf("discoveredTools[%d].Name() = empty", i)
		}
	}
}

func TestLiveSoulWithMCPTools(t *testing.T) {
	if strings.TrimSpace(os.Getenv("KIMI_API_KEY")) == "" {
		t.Skip("KIMI_API_KEY is not set, skipping live MCP + Soul test")
	}

	command, args := liveMCPServerCommand(t)
	loader := mcp.NewMCPToolLoader([]mcp.MCPServerConfig{
		{
			Name:      "live-mcp",
			Transport: mcp.TransportStdio,
			Command:   command,
			Args:      args,
		},
	})
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Fatalf("loader.Close() error = %v", err)
		}
	})

	loaderCtx, loaderCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer loaderCancel()
	discoveredTools, err := loader.LoadAll(loaderCtx)
	if err != nil {
		t.Skipf("live MCP stdio server unavailable (%s %v): %v", command, args, err)
	}
	if len(discoveredTools) == 0 {
		t.Fatalf("len(discoveredTools) = 0, want >= 1")
	}

	mcpEchoTool, ok := findMCPEchoTool(discoveredTools)
	if !ok {
		t.Skip("live MCP server has no echo tool; set MCP_E2E_STDIO_COMMAND/MCP_E2E_STDIO_ARGS to a server exposing echo")
	}
	toolName := mcpEchoTool.Name()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	provider := newM4LiveProvider(t, ctx)
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(mcpEchoTool),
		wire.NoopEmitter{},
		"You must call the MCP echo tool exactly once before the final response.",
	)

	const token = "LIVE_MCP_TOOL_TOKEN_2026"
	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "Call tool " + toolName + " exactly once with argument message=\"" + token + "\". After tool returns, reply with token only."},
	})
	liveRunOrSkip(t, err)

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if !containsCaseFold(output, token) {
		t.Fatalf("live response = %q, want contains %q", output, token)
	}
	if !contextHasToolCallName(ctxStore.Messages(), toolName) {
		t.Fatalf("context does not contain tool call %q: %#v", toolName, ctxStore.Messages())
	}
	if !contextHasToolOutputContains(ctxStore.Messages(), token) {
		t.Fatalf("context does not contain tool output token %q: %#v", token, ctxStore.Messages())
	}
}

func liveMCPServerCommand(t *testing.T) (string, []string) {
	t.Helper()

	command := strings.TrimSpace(os.Getenv(liveMCPCommandEnv))
	if command != "" {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s=%q is not executable: %v", liveMCPCommandEnv, command, err)
		}
		return command, strings.Fields(strings.TrimSpace(os.Getenv(liveMCPArgsEnv)))
	}

	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not found; set MCP_E2E_STDIO_COMMAND/MCP_E2E_STDIO_ARGS for a local MCP stdio server")
	}

	return "npx", []string{"-y", "@modelcontextprotocol/server-everything"}
}

func findMCPEchoTool(discoveredTools []tools.Tool) (tools.Tool, bool) {
	for i := range discoveredTools {
		name := strings.TrimSpace(discoveredTools[i].Name())
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "echo") || strings.HasSuffix(strings.ToLower(name), "__echo") {
			return discoveredTools[i], true
		}
	}
	return nil, false
}
