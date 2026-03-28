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
	"sync"
	"sync/atomic"
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

func TestStdioTransportSendSuccessWithZeroResponseID(t *testing.T) {
	t.Parallel()

	transport := newTestStdioTransport(t, "success")
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	atomic.StoreInt64(&transport.nextID, -1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := transport.Send(ctx, "tools/list", map[string]any{"cursor": "c0"})
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

func TestStdioTransportStderrBufferBounded(t *testing.T) {
	t.Parallel()

	transport := &StdioTransport{}
	writer := stderrBufferWriter{transport: transport}

	chunk := []byte(strings.Repeat("x", stderrTailLimit))
	if n, err := writer.Write(chunk); err != nil || n != len(chunk) {
		t.Fatalf("first Write() = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if n, err := writer.Write(chunk); err != nil || n != len(chunk) {
		t.Fatalf("second Write() = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if got, want := transport.stderrBuf.Len(), stderrTailLimit*2; got != want {
		t.Fatalf("stderr buffer len before overflow = %d, want %d", got, want)
	}

	if n, err := writer.Write([]byte("y")); err != nil || n != 1 {
		t.Fatalf("overflow Write() = (%d, %v), want (1, nil)", n, err)
	}
	if got, want := transport.stderrBuf.Len(), stderrTailLimit; got != want {
		t.Fatalf("stderr buffer len after overflow = %d, want %d", got, want)
	}
	if tail := transport.stderrTail(); len(tail) == 0 || !strings.HasSuffix(tail, "y") {
		t.Fatalf("stderrTail() = %q, want suffix %q", tail, "y")
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

func TestSSETransportSendJSONSuccessWithZeroResponseID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"ok\":true}}", req.ID)
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

	atomic.StoreInt64(&transport.nextID, -1)

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

func TestParseSSEResponseAcceptsZeroResponseID(t *testing.T) {
	t.Parallel()

	body := strings.NewReader(strings.Join([]string{
		`data: {"jsonrpc":"2.0","method":"notifications/progress","params":{"value":1}}`,
		``,
		`data: {"jsonrpc":"2.0","id":0,"result":{"ok":true}}`,
		``,
	}, "\n"))

	result, err := parseSSEResponse(body, 0)
	if err != nil {
		t.Fatalf("parseSSEResponse() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}
	if got, _ := decoded["ok"].(bool); !got {
		t.Fatalf("result.ok = %#v, want true", decoded["ok"])
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

func TestSSETransportCloseRejectsSendAndNotify(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport, err := NewSSETransport(server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := transport.Send(ctx, "tools/list", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Send() error = %v, want closed error", err)
	}
	if err := transport.Notify(ctx, "notifications/initialized", map[string]any{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Notify() error = %v, want closed error", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests sent after Close = %d, want 0", got)
	}
}

func TestSSETransportSendCloseConcurrent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"ok\":true}}", req.ID)
	}))
	defer server.Close()

	transport, err := NewSSETransport(server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	t.Cleanup(func() {
		_ = transport.Close()
	})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := transport.Send(ctx, "tools/list", map[string]any{"cursor": "c1"})
			if err != nil && !strings.Contains(err.Error(), "closed") {
				t.Errorf("Send() unexpected error = %v", err)
			}
		}()
	}

	time.Sleep(10 * time.Millisecond)
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := transport.Send(ctx, "tools/list", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Send() after Close error = %v, want closed error", err)
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
		case "rpc_error":
			_, _ = fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"error\":{\"code\":-32000,\"message\":\"boom\"}}\n", req.ID)
			_ = writer.Flush()
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
