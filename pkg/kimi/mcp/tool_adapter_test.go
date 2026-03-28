package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMCPToolNameAndMetadata(t *testing.T) {
	t.Parallel()

	tool := NewMCPTool(nil, MCPToolDefinition{
		Name:        " echo ",
		Description: " demo ",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, " server ")

	if got, want := tool.Name(), "server__echo"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if got, want := tool.Description(), "demo"; got != want {
		t.Fatalf("Description() = %q, want %q", got, want)
	}
	if got := string(tool.ParameterSchema()); got != `{"type":"object"}` {
		t.Fatalf("ParameterSchema() = %s, want object schema", got)
	}

	toolNoPrefix := NewMCPTool(nil, MCPToolDefinition{Name: "echo"}, "")
	if got, want := toolNoPrefix.Name(), "echo"; got != want {
		t.Fatalf("Name() without server = %q, want %q", got, want)
	}
}

func TestMCPToolExecuteSuccess(t *testing.T) {
	t.Parallel()

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
		},
		"tools/call": {
			{result: json.RawMessage(`{"content":[{"type":"text","text":"hello"}],"isError":false}`)},
		},
	})
	transport := newMockTransportWithNotify(base, nil)
	client := NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tool := NewMCPTool(client, MCPToolDefinition{
		Name:        "echo",
		Description: "echo text",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, "fs")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := result.Name, "fs__echo"; got != want {
		t.Fatalf("result.Name = %q, want %q", got, want)
	}
	if result.IsError {
		t.Fatal("result.IsError = true, want false")
	}

	valueJSON, err := json.Marshal(result.Value.Value)
	if err != nil {
		t.Fatalf("json.Marshal(result.Value) error = %v", err)
	}
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(valueJSON, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload) error = %v", err)
	}
	if payload.IsError {
		t.Fatal("payload.isError = true, want false")
	}
	if len(payload.Content) != 1 || payload.Content[0].Text != "hello" {
		t.Fatalf("payload.content = %#v, want single text hello", payload.Content)
	}

	callParams := decodeJSONMap(t, base.callParams(1))
	if got := callParams["name"]; got != "echo" {
		t.Fatalf("tools/call name = %#v, want echo", got)
	}
	args, ok := callParams["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call arguments type = %T, want map[string]any", callParams["arguments"])
	}
	if got := args["message"]; got != "hi" {
		t.Fatalf("tools/call arguments.message = %#v, want hi", got)
	}
}

func TestMCPToolExecuteErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil tool", func(t *testing.T) {
		t.Parallel()
		var tool *MCPTool
		_, err := tool.Execute(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "nil tool") {
			t.Fatalf("Execute(nil tool) error = %v, want nil tool", err)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		t.Parallel()
		tool := NewMCPTool(nil, MCPToolDefinition{Name: "echo"}, "fs")
		_, err := tool.Execute(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "nil client") {
			t.Fatalf("Execute(nil client) error = %v, want nil client", err)
		}
	})

	t.Run("invalid params", func(t *testing.T) {
		t.Parallel()
		base := newMockTransport(map[string][]mockSendResponse{
			"initialize": {
				{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
			},
		})
		transport := newMockTransportWithNotify(base, nil)
		client := NewMCPClient(transport)
		if err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}
		tool := NewMCPTool(client, MCPToolDefinition{Name: "echo"}, "fs")

		_, err := tool.Execute(context.Background(), json.RawMessage(`[]`))
		if err == nil || !strings.Contains(err.Error(), "decode params") {
			t.Fatalf("Execute(invalid params) error = %v, want decode params", err)
		}
	})

	t.Run("call tool error", func(t *testing.T) {
		t.Parallel()
		base := newMockTransport(map[string][]mockSendResponse{
			"initialize": {
				{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
			},
			"tools/call": {
				{err: &RPCError{Code: -32000, Message: "boom"}},
			},
		})
		transport := newMockTransportWithNotify(base, nil)
		client := NewMCPClient(transport)
		if err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}

		tool := NewMCPTool(client, MCPToolDefinition{Name: "echo"}, "fs")
		_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
		if err == nil {
			t.Fatal("Execute(call error) error = nil, want error")
		}
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			t.Fatalf("Execute(call error) error type = %T, want wrapped *RPCError", err)
		}
	})
}

func TestMCPToolExecuteHonorsPerToolTimeout(t *testing.T) {
	t.Parallel()

	transport := &blockingCallTransport{}
	client := NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tool := NewMCPToolWithTimeout(client, MCPToolDefinition{Name: "echo"}, "fs", 50*time.Millisecond)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Execute(timeout) error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Execute(timeout) error = %q, want deadline exceeded", err.Error())
	}
}

type blockingCallTransport struct{}

func (t *blockingCallTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch strings.TrimSpace(method) {
	case "initialize":
		return json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`), nil
	case "notifications/initialized":
		return json.RawMessage(`{}`), nil
	case "tools/call":
		<-ctx.Done()
		return nil, ctx.Err()
	default:
		return nil, errors.New("unexpected method")
	}
}

func (t *blockingCallTransport) Close() error {
	return nil
}
