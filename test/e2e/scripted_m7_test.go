//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/mcp"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

const scriptedM7MCPHelperEnv = "GO_KIMI_E2E_M7_MCP_HELPER"

func TestScriptedMCPStdioTransport(t *testing.T) {
	t.Parallel()

	transport := newScriptedM7StdioTransport(t, "transport")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := transport.Send(ctx, "custom/ping", map[string]any{"token": "SCRIPTED_M7_TRANSPORT_TOKEN_2026"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("result.ok = %#v, want true", payload["ok"])
	}
	if got, _ := payload["method"].(string); got != "custom/ping" {
		t.Fatalf("result.method = %q, want %q", got, "custom/ping")
	}
}

func TestScriptedMCPClientHandshake(t *testing.T) {
	t.Parallel()

	transport := newScriptedM7StdioTransport(t, "client")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	client := mcp.NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	serverInfo := client.ServerInfo()
	if serverInfo == nil {
		t.Fatal("ServerInfo() = nil, want non-nil")
	}
	if got := strings.TrimSpace(serverInfo.Name); got != "scripted-mcp" {
		t.Fatalf("ServerInfo().Name = %q, want %q", got, "scripted-mcp")
	}

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("second Initialize() error = %v", err)
	}
}

func TestScriptedMCPToolDiscovery(t *testing.T) {
	t.Parallel()

	transport := newScriptedM7StdioTransport(t, "discovery")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	client := mcp.NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	definitions, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("len(definitions) = %d, want 1", len(definitions))
	}
	if got := definitions[0].Name; got != "echo" {
		t.Fatalf("definitions[0].Name = %q, want %q", got, "echo")
	}
	if got := definitions[0].Description; got != "Echo back one message" {
		t.Fatalf("definitions[0].Description = %q, want %q", got, "Echo back one message")
	}
	if len(definitions[0].InputSchema) == 0 {
		t.Fatal("definitions[0].InputSchema = empty, want non-empty")
	}
}

func TestScriptedMCPToolCall(t *testing.T) {
	t.Parallel()

	transport := newScriptedM7StdioTransport(t, "tool-call")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	client := mcp.NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const token = "SCRIPTED_M7_TOOL_CALL_TOKEN_2026"
	result, err := client.CallTool(context.Background(), "echo", map[string]any{"message": token})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result == nil {
		t.Fatal("CallTool() result = nil, want non-nil")
	}
	if result.IsError {
		t.Fatalf("CallTool().IsError = true, want false; content=%#v", result.Content)
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(result.Content) = %d, want 1", len(result.Content))
	}
	if got := result.Content[0].Type; got != "text" {
		t.Fatalf("result.Content[0].Type = %q, want %q", got, "text")
	}
	if got := result.Content[0].Text; got != "echo:"+token {
		t.Fatalf("result.Content[0].Text = %q, want %q", got, "echo:"+token)
	}
}

func TestScriptedMCPToolRegistration(t *testing.T) {
	t.Parallel()

	loader := mcp.NewMCPToolLoader([]mcp.MCPServerConfig{
		{
			Name:      "scripted",
			Transport: mcp.TransportStdio,
			Command:   os.Args[0],
			Args:      []string{"-test.run=TestScriptedMCPHelperProcess", "--", "registration"},
			Env: map[string]string{
				scriptedM7MCPHelperEnv: "1",
			},
		},
	})
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Fatalf("loader.Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	discoveredTools, err := loader.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if len(discoveredTools) != 1 {
		t.Fatalf("len(discoveredTools) = %d, want 1", len(discoveredTools))
	}

	registry := tools.NewMapToolRegistry(discoveredTools...)
	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("len(registry.Definitions()) = %d, want 1", len(definitions))
	}
	toolName := definitions[0].Name
	if toolName != "scripted__echo" {
		t.Fatalf("registry tool name = %q, want %q", toolName, "scripted__echo")
	}

	const token = "SCRIPTED_M7_REGISTRY_TOKEN_2026"
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-scripted-m7-mcp",
						Name: toolName,
						Arguments: map[string]any{
							"message": token,
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "mcp registry flow completed"}},
				{Done: true},
			},
		},
	}

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, registry, wire.NoopEmitter{}, "")
	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "call the scripted mcp tool"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "mcp registry flow completed" {
		t.Fatalf("result text = %q, want %q", got, "mcp registry flow completed")
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider.CallCount() = %d, want 2", provider.CallCount())
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	toolOutput := textFromContentParts(messages[2].Content)
	if !strings.Contains(toolOutput, "echo:"+token) {
		t.Fatalf("tool output = %q, want contains %q", toolOutput, "echo:"+token)
	}
}

func TestScriptedMCPHelperProcess(t *testing.T) {
	if os.Getenv(scriptedM7MCPHelperEnv) != "1" {
		return
	}

	scenario := m7HelperScenarioFromArgs(os.Args)
	if strings.TrimSpace(scenario) == "" {
		fmt.Fprintln(os.Stderr, "missing m7 helper scenario")
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req mcp.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = m7WriteRPCError(writer, 1, -32700, "parse error")
			os.Exit(0)
		}

		method := strings.TrimSpace(req.Method)
		params, _ := req.Params.(map[string]any)

		// Notifications have no request id and should not get responses.
		if req.ID == 0 {
			continue
		}

		switch method {
		case "initialize":
			err := m7WriteRPCResult(writer, req.ID, map[string]any{
				"protocolVersion": "2026-03-26",
				"capabilities":    map[string]any{},
				"serverInfo": map[string]any{
					"name":    "scripted-mcp",
					"version": "2026.03",
				},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "write initialize response: %v\n", err)
				os.Exit(1)
			}
		case "tools/list":
			err := m7WriteRPCResult(writer, req.ID, map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"description": "Echo back one message",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{
									"type": "string",
								},
							},
							"required": []string{"message"},
						},
					},
				},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "write tools/list response: %v\n", err)
				os.Exit(1)
			}
		case "tools/call":
			name, _ := params["name"].(string)
			name = strings.TrimSpace(name)
			if name != "echo" {
				_ = m7WriteRPCError(writer, req.ID, -32601, "tool not found")
				continue
			}
			arguments, _ := params["arguments"].(map[string]any)
			message := strings.TrimSpace(fmt.Sprintf("%v", arguments["message"]))
			err := m7WriteRPCResult(writer, req.ID, map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "echo:" + message,
					},
				},
				"isError": false,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "write tools/call response: %v\n", err)
				os.Exit(1)
			}
		default:
			err := m7WriteRPCResult(writer, req.ID, map[string]any{
				"ok":     true,
				"method": method,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "write default response: %v\n", err)
				os.Exit(1)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "helper scanner error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func newScriptedM7StdioTransport(t *testing.T, scenario string) *mcp.StdioTransport {
	t.Helper()

	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		scenario = "default"
	}

	transport, err := mcp.NewStdioTransport(
		os.Args[0],
		[]string{"-test.run=TestScriptedMCPHelperProcess", "--", scenario},
		map[string]string{scriptedM7MCPHelperEnv: "1"},
	)
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}
	return transport
}

func m7WriteRPCResult(writer *bufio.Writer, id int, result any) error {
	return m7WriteRPCPayload(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func m7WriteRPCError(writer *bufio.Writer, id int, code int, message string) error {
	return m7WriteRPCPayload(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func m7WriteRPCPayload(writer *bufio.Writer, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

func m7HelperScenarioFromArgs(args []string) string {
	for i := range args {
		if args[i] == "--" && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
	}
	return ""
}
