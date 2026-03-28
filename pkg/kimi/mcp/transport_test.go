package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const stdioHelperEnv = "GO_KIMI_MCP_STDIO_HELPER"

func TestStdioTransportSendSuccess(t *testing.T) {
	t.Parallel()

	transport := newTestStdioTransport(t, "success")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := transport.Send(ctx, "tools/list", map[string]any{"cursor": "c1"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}
	if ok, _ := decoded["ok"].(bool); !ok {
		t.Fatalf("result.ok = %#v, want true", decoded["ok"])
	}
	if got, _ := decoded["method"].(string); got != "tools/list" {
		t.Fatalf("result.method = %q, want %q", got, "tools/list")
	}
}

func TestStdioTransportSendRPCError(t *testing.T) {
	t.Parallel()

	transport := newTestStdioTransport(t, "rpc_error")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := transport.Send(ctx, "tools/call", map[string]any{"name": "echo"})
	if err == nil {
		t.Fatal("Send() error = nil, want rpc error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Send() error type = %T, want *RPCError", err)
	}
	if rpcErr.Code != -32000 {
		t.Fatalf("rpc error code = %d, want -32000", rpcErr.Code)
	}
}

func TestStdioTransportSendContextDeadline(t *testing.T) {
	t.Parallel()

	transport := newTestStdioTransport(t, "no_response")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := transport.Send(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("Send() error = nil, want context deadline exceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send() error = %v, want context deadline exceeded", err)
	}
}

func TestSSETransportSendSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want text/event-stream", got)
		}
		if got := r.Header.Get("X-API-Key"); got != "token-123" {
			t.Fatalf("X-API-Key = %q, want token-123", got)
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.JSONRPC != jsonRPCVersion {
			t.Fatalf("request.jsonrpc = %q, want %q", req.JSONRPC, jsonRPCVersion)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support flush")
		}

		_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"value\":1}}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"ok\":true}}\n\n", req.ID)
		flusher.Flush()
	}))
	defer server.Close()

	transport, err := NewSSETransport(server.URL, map[string]string{
		"X-API-Key": "token-123",
	})
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := transport.Send(ctx, "initialize", map[string]any{"protocolVersion": "2026-03-26"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s, want %s", string(result), `{"ok":true}`)
	}
}

func TestSSETransportSendRPCError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support flush")
		}
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"error\":{\"code\":-32601,\"message\":\"method not found\"}}\n\n", req.ID)
		flusher.Flush()
	}))
	defer server.Close()

	transport, err := NewSSETransport(server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = transport.Send(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("Send() error = nil, want rpc error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Send() error type = %T, want *RPCError", err)
	}
	if rpcErr.Code != -32601 {
		t.Fatalf("rpc error code = %d, want -32601", rpcErr.Code)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv(stdioHelperEnv) != "1" {
		return
	}

	scenario := helperScenarioFromArgs(os.Args)
	if strings.TrimSpace(scenario) == "" {
		fmt.Fprintln(os.Stderr, "missing helper scenario")
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_, _ = fmt.Fprintln(writer, `{"jsonrpc":"2.0","id":1,"error":{"code":-32700,"message":"parse error"}}`)
			_ = writer.Flush()
			os.Exit(0)
		}

		switch scenario {
		case "success":
			_, _ = fmt.Fprintln(writer, `{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":10}}`)
			_, _ = fmt.Fprintf(
				writer,
				"{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"ok\":true,\"method\":%q}}\n",
				req.ID,
				req.Method,
			)
			_ = writer.Flush()
			os.Exit(0)
		case "rpc_error":
			_, _ = fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"error\":{\"code\":-32000,\"message\":\"boom\"}}\n", req.ID)
			_ = writer.Flush()
			os.Exit(0)
		case "no_response":
			select {}
		default:
			_, _ = fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"error\":{\"code\":-32099,\"message\":\"unknown scenario\"}}\n", req.ID)
			_ = writer.Flush()
			os.Exit(0)
		}
	}

	os.Exit(0)
}

func newTestStdioTransport(t *testing.T, scenario string) *StdioTransport {
	t.Helper()

	transport, err := NewStdioTransport(
		os.Args[0],
		[]string{"-test.run=TestMCPStdioHelperProcess", "--", scenario},
		map[string]string{stdioHelperEnv: "1"},
	)
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}
	return transport
}

func helperScenarioFromArgs(args []string) string {
	for i := range args {
		if args[i] == "--" && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
	}
	return ""
}
