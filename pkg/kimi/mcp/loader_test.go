package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestMCPToolLoaderLoadAllSuccess(t *testing.T) {
	t.Parallel()

	stdioBase := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"stdio-server"}}`)},
		},
		"tools/list": {
			{result: json.RawMessage(`{"tools":[{"name":"echo","description":"echo text","inputSchema":{"type":"object"}}]}`)},
		},
	})
	stdioTransport := newMockTransportWithNotify(stdioBase, nil)

	sseBase := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"sse-server"}}`)},
		},
		"tools/list": {
			{result: json.RawMessage(`{"tools":[{"name":"search","description":"search web","inputSchema":{"type":"object"}}]}`)},
		},
	})
	sseTransport := newMockTransportWithNotify(sseBase, nil)

	loader := NewMCPToolLoader([]MCPServerConfig{
		{Name: " stdio-server ", Transport: TransportType(" STDIO "), Command: " cmd "},
		{Name: "sse-server", Transport: TransportSSE, URL: "http://localhost"},
	})

	calls := make([]MCPServerConfig, 0, 2)
	loader.transportFactory = func(cfg MCPServerConfig) (Transport, error) {
		calls = append(calls, cfg)
		switch cfg.Name {
		case "stdio-server":
			return stdioTransport, nil
		case "sse-server":
			return sseTransport, nil
		default:
			return nil, errors.New("unexpected server")
		}
	}

	tools, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}

	if len(calls) != 2 {
		t.Fatalf("transportFactory calls = %d, want 2", len(calls))
	}
	if calls[0].Transport != TransportStdio || calls[0].Command != "cmd" {
		t.Fatalf("calls[0] normalized config = %#v, want stdio + trimmed command", calls[0])
	}

	names := []string{tools[0].Name(), tools[1].Name()}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "sse-server__search,stdio-server__echo"; got != want {
		t.Fatalf("tool names = %q, want %q", got, want)
	}

	if err := loader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !stdioBase.closed || !sseBase.closed {
		t.Fatalf("transport closed flags = stdio:%v sse:%v, want both true", stdioBase.closed, sseBase.closed)
	}
}

func TestMCPToolLoaderLoadAllFailureClosesClients(t *testing.T) {
	t.Parallel()

	goodBase := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{result: json.RawMessage(`{"protocolVersion":"2026-03-26","serverInfo":{"name":"a"}}`)},
		},
		"tools/list": {
			{result: json.RawMessage(`{"tools":[]}`)},
		},
	})
	goodTransport := newMockTransportWithNotify(goodBase, nil)

	badBase := newMockTransport(map[string][]mockSendResponse{
		"initialize": {
			{err: errors.New("init failed")},
		},
	})
	badTransport := newMockTransportWithNotify(badBase, nil)

	loader := NewMCPToolLoader([]MCPServerConfig{
		{Name: "a", Transport: TransportStdio, Command: "cmd-a"},
		{Name: "b", Transport: TransportStdio, Command: "cmd-b"},
	})

	loader.transportFactory = func(cfg MCPServerConfig) (Transport, error) {
		if cfg.Name == "a" {
			return goodTransport, nil
		}
		return badTransport, nil
	}

	_, err := loader.LoadAll(context.Background())
	if err == nil {
		t.Fatal("LoadAll() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `initialize server "b"`) {
		t.Fatalf("LoadAll() error = %q, want initialize server b", err.Error())
	}
	if !goodBase.closed {
		t.Fatal("first client transport should be closed on failure")
	}
	if !badBase.closed {
		t.Fatal("failed client transport should be closed on failure")
	}

	loader.mu.Lock()
	defer loader.mu.Unlock()
	if len(loader.clients) != 0 {
		t.Fatalf("loader.clients should be empty on failed LoadAll, got %d", len(loader.clients))
	}
}

func TestMCPToolLoaderCloseAggregatesErrors(t *testing.T) {
	t.Parallel()

	loader := NewMCPToolLoader(nil)
	loader.clients = []*MCPClient{
		NewMCPClient(&mockTransport{closeErr: errors.New("close a")}),
		NewMCPClient(&mockTransport{}),
		NewMCPClient(&mockTransport{closeErr: errors.New("close b")}),
	}

	err := loader.Close()
	if err == nil {
		t.Fatal("Close() error = nil, want aggregated close error")
	}
	if !strings.Contains(err.Error(), "close a") || !strings.Contains(err.Error(), "close b") {
		t.Fatalf("Close() error = %q, want both close errors", err.Error())
	}

	loader.mu.Lock()
	defer loader.mu.Unlock()
	if len(loader.clients) != 0 {
		t.Fatalf("loader.clients should be cleared, got %d", len(loader.clients))
	}
}

func TestMCPToolLoaderLoadAllRejectsDuplicateNames(t *testing.T) {
	t.Parallel()

	loader := NewMCPToolLoader([]MCPServerConfig{
		{Name: "FS", Transport: TransportStdio, Command: "cmd"},
		{Name: "fs", Transport: TransportSSE, URL: "http://localhost"},
	})

	_, err := loader.LoadAll(context.Background())
	if err == nil {
		t.Fatal("LoadAll() duplicate names error = nil, want error")
	}
	if !strings.Contains(err.Error(), "duplicate server name") {
		t.Fatalf("LoadAll() error = %q, want duplicate server name", err.Error())
	}
}
