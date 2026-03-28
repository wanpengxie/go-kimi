package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type mockSendResponse struct {
	result json.RawMessage
	err    error
}

type mockCall struct {
	method string
	params json.RawMessage
}

type mockTransport struct {
	mu        sync.Mutex
	responses map[string][]mockSendResponse
	sendCalls []mockCall
	closed    bool
	closeErr  error
}

func newMockTransport(responses map[string][]mockSendResponse) *mockTransport {
	copied := make(map[string][]mockSendResponse, len(responses))
	for method, queue := range responses {
		cloned := make([]mockSendResponse, len(queue))
		for i := range queue {
			cloned[i] = mockSendResponse{
				result: cloneRawMessage(queue[i].result),
				err:    queue[i].err,
			}
		}
		copied[method] = cloned
	}
	return &mockTransport{responses: copied}
}

func (m *mockTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mock transport: marshal params: %w", err)
	}
	m.sendCalls = append(m.sendCalls, mockCall{method: method, params: cloneRawMessage(raw)})

	queue := m.responses[method]
	if len(queue) == 0 {
		return nil, fmt.Errorf("mock transport: unexpected method %q", method)
	}
	resp := queue[0]
	m.responses[method] = queue[1:]
	if resp.err != nil {
		return nil, resp.err
	}
	return cloneRawMessage(resp.result), nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return m.closeErr
}

func (m *mockTransport) methods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sendCalls))
	for i := range m.sendCalls {
		out[i] = m.sendCalls[i].method
	}
	return out
}

func (m *mockTransport) callParams(index int) json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.sendCalls) {
		return nil
	}
	return cloneRawMessage(m.sendCalls[index].params)
}

type mockTransportWithNotify struct {
	*mockTransport

	notifyMu    sync.Mutex
	notifyErr   map[string]error
	notifyCalls []mockCall
}

func newMockTransportWithNotify(base *mockTransport, notifyErr map[string]error) *mockTransportWithNotify {
	copied := make(map[string]error, len(notifyErr))
	for method, err := range notifyErr {
		copied[method] = err
	}
	return &mockTransportWithNotify{
		mockTransport: base,
		notifyErr:     copied,
	}
}

func (m *mockTransportWithNotify) Notify(ctx context.Context, method string, params any) error {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mock transport: marshal notify params: %w", err)
	}
	m.notifyCalls = append(m.notifyCalls, mockCall{method: method, params: cloneRawMessage(raw)})
	if notifyErr, ok := m.notifyErr[method]; ok {
		return notifyErr
	}
	return nil
}

func (m *mockTransportWithNotify) notifyMethods() []string {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	out := make([]string, len(m.notifyCalls))
	for i := range m.notifyCalls {
		out[i] = m.notifyCalls[i].method
	}
	return out
}

func TestMCPClientInitializeSuccess(t *testing.T) {
	t.Parallel()

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","capabilities":{},"serverInfo":{"name":"fs","version":"1.0.0"}}`)},
		},
	})
	transport := newMockTransportWithNotify(base, nil)

	client := NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if got, want := base.methods(), []string{"initialize"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("send methods = %#v, want %#v", got, want)
	}
	if got, want := transport.notifyMethods(), []string{"notifications/initialized"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("notify methods = %#v, want %#v", got, want)
	}

	params := decodeJSONMap(t, base.callParams(0))
	if got := params["protocolVersion"]; got != defaultMCPProtocolVersion {
		t.Fatalf("initialize.protocolVersion = %#v, want %q", got, defaultMCPProtocolVersion)
	}
	clientInfo, ok := params["clientInfo"].(map[string]any)
	if !ok {
		t.Fatalf("initialize.clientInfo type = %T, want map[string]any", params["clientInfo"])
	}
	if got := clientInfo["name"]; got != defaultMCPClientName {
		t.Fatalf("initialize.clientInfo.name = %#v, want %q", got, defaultMCPClientName)
	}

	server := client.ServerInfo()
	if server == nil || server.Name != "fs" || server.Version != "1.0.0" {
		t.Fatalf("ServerInfo() = %#v, want name=fs version=1.0.0", server)
	}

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("second Initialize() error = %v", err)
	}
	if got, want := base.methods(), []string{"initialize"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second initialize send methods = %#v, want %#v", got, want)
	}
}

func TestMCPClientInitializeFallsBackToSendNotification(t *testing.T) {
	t.Parallel()

	transport := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","capabilities":{},"serverInfo":{"name":"fs"}}`)},
		},
		"notifications/initialized": {
			{result: json.RawMessage(`{}`)},
		},
	})

	client := NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if got, want := transport.methods(), []string{"initialize", "notifications/initialized"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("send methods = %#v, want %#v", got, want)
	}
}

func TestMCPClientInitializeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		transport  Transport
		wantSubstr string
	}{
		{
			name:       "nil transport",
			transport:  nil,
			wantSubstr: "nil transport",
		},
		{
			name: "missing protocol version",
			transport: newMockTransportWithNotify(newMockTransport(map[string][]mockSendResponse{
				"initialize": {
					{result: json.RawMessage(`{"serverInfo":{"name":"fs"}}`)},
				},
			}), nil),
			wantSubstr: "protocolVersion",
		},
		{
			name: "missing server info",
			transport: newMockTransportWithNotify(newMockTransport(map[string][]mockSendResponse{
				"initialize": {
					{result: json.RawMessage(`{"protocolVersion":"2026-03-26"}`)},
				},
			}), nil),
			wantSubstr: "serverInfo",
		},
		{
			name: "initialized notification error",
			transport: newMockTransportWithNotify(newMockTransport(map[string][]mockSendResponse{
				"initialize": {
					{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
				},
			}), map[string]error{"notifications/initialized": errors.New("notify failed")}),
			wantSubstr: "notifications/initialized",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := NewMCPClient(tc.transport)
			err := client.Initialize(context.Background())
			if err == nil {
				t.Fatal("Initialize() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("Initialize() error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestMCPClientListToolsSuccess(t *testing.T) {
	t.Parallel()

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","capabilities":{},"serverInfo":{"name":"fs"}}`)},
		},
		"tools/list": {
			{result: json.RawMessage(`{"tools":[{"name":" echo ","description":" testing ","inputSchema":{"type":"object","properties":{"path":{"type":"string"}}}}]}`)},
		},
	})
	transport := newMockTransportWithNotify(base, nil)
	client := NewMCPClient(transport)

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if got, want := len(tools), 1; got != want {
		t.Fatalf("len(tools) = %d, want %d", got, want)
	}
	if tools[0].Name != "echo" {
		t.Fatalf("tool.name = %q, want %q", tools[0].Name, "echo")
	}
	if tools[0].Description != "testing" {
		t.Fatalf("tool.description = %q, want %q", tools[0].Description, "testing")
	}
	if string(tools[0].InputSchema) == "" {
		t.Fatal("tool.inputSchema should not be empty")
	}

	if got, want := base.methods(), []string{"initialize", "tools/list"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("send methods = %#v, want %#v", got, want)
	}

	listParams := decodeJSONMap(t, base.callParams(1))
	if len(listParams) != 0 {
		t.Fatalf("tools/list params = %#v, want empty object", listParams)
	}
}

func TestMCPClientListToolsErrors(t *testing.T) {
	t.Parallel()

	client := NewMCPClient(newMockTransport(nil))
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools() before Initialize expected error")
	}

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
		},
		"tools/list": {
			{result: json.RawMessage(`{"tools":[{"name":"   "}]}`)},
		},
	})
	transport := newMockTransportWithNotify(base, nil)
	client = NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools() expected error for empty tool name")
	}
}

func TestMCPClientCallToolSuccess(t *testing.T) {
	t.Parallel()

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
		},
		"tools/call": {
			{result: json.RawMessage(`{"content":[{"type":" text ","text":" hello "}],"isError":false}`)},
		},
	})
	transport := newMockTransportWithNotify(base, nil)
	client := NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	result, err := client.CallTool(context.Background(), "echo", map[string]any{"path": "/tmp/a"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result == nil {
		t.Fatal("CallTool() result = nil")
	}
	if result.IsError {
		t.Fatal("CallTool().IsError = true, want false")
	}
	if got, want := len(result.Content), 1; got != want {
		t.Fatalf("len(result.Content) = %d, want %d", got, want)
	}
	if got, want := result.Content[0].Type, "text"; got != want {
		t.Fatalf("result.Content[0].Type = %q, want %q", got, want)
	}
	if got, want := result.Content[0].Text, "hello"; got != want {
		t.Fatalf("result.Content[0].Text = %q, want %q", got, want)
	}

	params := decodeJSONMap(t, base.callParams(1))
	if got := params["name"]; got != "echo" {
		t.Fatalf("tools/call params.name = %#v, want %q", got, "echo")
	}
	arguments, ok := params["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call params.arguments type = %T, want map[string]any", params["arguments"])
	}
	if got := arguments["path"]; got != "/tmp/a" {
		t.Fatalf("tools/call params.arguments.path = %#v, want %q", got, "/tmp/a")
	}
}

func TestMCPClientCallToolErrors(t *testing.T) {
	t.Parallel()

	client := NewMCPClient(newMockTransport(nil))
	if _, err := client.CallTool(context.Background(), "echo", nil); err == nil {
		t.Fatal("CallTool() before Initialize expected error")
	}

	base := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"fs"}}`)},
		},
		"tools/call": {
			{err: &RPCError{Code: -32000, Message: "tool failed"}},
		},
	})
	transport := newMockTransportWithNotify(base, nil)
	client = NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if _, err := client.CallTool(context.Background(), " ", nil); err == nil {
		t.Fatal("CallTool() with empty tool name expected error")
	}

	_, err := client.CallTool(context.Background(), "echo", nil)
	if err == nil {
		t.Fatal("CallTool() rpc error expected")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("CallTool() error type = %T, want *RPCError", err)
	}
	if rpcErr.Code != -32000 {
		t.Fatalf("rpc error code = %d, want -32000", rpcErr.Code)
	}
}

func TestMCPClientClose(t *testing.T) {
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
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !base.closed {
		t.Fatal("transport.Close() was not called")
	}
	if info := client.ServerInfo(); info != nil {
		t.Fatalf("ServerInfo() after Close = %#v, want nil", info)
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools() after Close expected initialize-required error")
	}
}

func decodeJSONMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", string(raw), err)
	}
	if decoded == nil {
		return map[string]any{}
	}
	return decoded
}
