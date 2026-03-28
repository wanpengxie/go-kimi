package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	jsonRPCVersion = "2.0"

	stdioStartTimeout    = 10 * time.Second
	stdioShutdownTimeout = 2 * time.Second
	stdioScannerMaxSize  = 4 * 1024 * 1024
	stderrTailLimit      = 4096

	sseRequestTimeout = 30 * time.Second
	sseScannerMaxSize = 4 * 1024 * 1024
)

// Transport abstracts one MCP JSON-RPC transport channel.
type Transport interface {
	// Send sends one JSON-RPC request and returns the response result payload.
	Send(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Close closes transport resources.
	Close() error
}

// Request is one JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type notificationRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is one JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is one JSON-RPC 2.0 error payload.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "mcp rpc error: <nil>"
	}
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, strings.TrimSpace(e.Message))
}

var _ Transport = (*StdioTransport)(nil)
var _ Transport = (*SSETransport)(nil)

type stdioResult struct {
	response Response
	err      error
}

// StdioTransport sends JSON-RPC over one child process stdin/stdout.
type StdioTransport struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	mu      sync.Mutex
	closed  bool
	readErr error
	nextID  int64
	pending map[int]chan stdioResult
	waitErr error

	waitDone  chan struct{}
	closeOnce sync.Once

	stderrMu  sync.Mutex
	stderrBuf bytes.Buffer
}

// NewStdioTransport starts one child process and binds stdin/stdout JSON-RPC transport.
func NewStdioTransport(command string, args []string, env map[string]string) (*StdioTransport, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("mcp stdio transport: command is required")
	}

	cmd := exec.Command(command, cloneStrings(args)...)
	cmd.Env = buildProcessEnv(env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio transport: setup stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio transport: setup stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio transport: setup stderr: %w", err)
	}

	if err := startCommandWithTimeout(cmd, stdioStartTimeout); err != nil {
		return nil, err
	}

	transport := &StdioTransport{
		cmd:      cmd,
		stdin:    stdin,
		pending:  make(map[int]chan stdioResult),
		waitDone: make(chan struct{}),
	}
	go transport.captureStderr(stderr)
	go transport.readLoop(stdout)
	go transport.waitLoop()
	return transport, nil
}

// Send sends one JSON-RPC request and waits for its response.
func (t *StdioTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return nil, errors.New("mcp stdio transport: nil transport")
	}

	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("mcp stdio transport: method is required")
	}

	id := int(atomic.AddInt64(&t.nextID, 1))
	request := Request{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("mcp stdio transport: marshal request: %w", err)
	}

	resultCh := make(chan stdioResult, 1)
	if err := t.registerPending(id, resultCh); err != nil {
		return nil, t.withStderr(err)
	}
	defer t.unregisterPending(id)

	t.writeMu.Lock()
	_, err = t.stdin.Write(append(payload, '\n'))
	t.writeMu.Unlock()
	if err != nil {
		return nil, t.withStderr(fmt.Errorf("mcp stdio transport: write request: %w", err))
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("mcp stdio transport: wait response: %w", ctx.Err())
	case result := <-resultCh:
		if result.err != nil {
			return nil, t.withStderr(result.err)
		}
		if result.response.Error != nil {
			return nil, result.response.Error
		}
		return cloneRawMessage(result.response.Result), nil
	}
}

// Notify sends one JSON-RPC notification without waiting for a response.
func (t *StdioTransport) Notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return errors.New("mcp stdio transport: nil transport")
	}

	method = strings.TrimSpace(method)
	if method == "" {
		return errors.New("mcp stdio transport: method is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mcp stdio transport: notify: %w", err)
	}

	payload, err := json.Marshal(notificationRequest{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("mcp stdio transport: marshal notification: %w", err)
	}

	t.mu.Lock()
	closed := t.closed
	readErr := t.readErr
	t.mu.Unlock()
	if closed {
		return errors.New("mcp stdio transport: closed")
	}
	if readErr != nil {
		return t.withStderr(readErr)
	}

	t.writeMu.Lock()
	_, err = t.stdin.Write(append(payload, '\n'))
	t.writeMu.Unlock()
	if err != nil {
		return t.withStderr(fmt.Errorf("mcp stdio transport: write notification: %w", err))
	}
	return nil
}

// Close gracefully terminates the child process.
func (t *StdioTransport) Close() error {
	if t == nil {
		return nil
	}

	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.failAllPending(errors.New("mcp stdio transport: closed"))

		if t.stdin != nil {
			_ = t.stdin.Close()
		}

		proc := t.cmd.Process
		if proc != nil {
			_ = proc.Signal(syscall.SIGTERM)
		}

		timer := time.NewTimer(stdioShutdownTimeout)
		defer timer.Stop()
		select {
		case <-t.waitDone:
		case <-timer.C:
			if proc != nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
			<-t.waitDone
		}
	})
	return nil
}

func (t *StdioTransport) registerPending(id int, ch chan stdioResult) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return errors.New("mcp stdio transport: closed")
	}
	if t.readErr != nil {
		return t.readErr
	}
	t.pending[id] = ch
	return nil
}

func (t *StdioTransport) unregisterPending(id int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, id)
}

func (t *StdioTransport) readLoop(stdout io.ReadCloser) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), stdioScannerMaxSize)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var response Response
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.failAllPending(fmt.Errorf("mcp stdio transport: decode response: %w", err))
			return
		}
		if response.ID == 0 {
			// Notification messages do not carry an id and are ignored in transport layer.
			continue
		}
		t.dispatchResponse(response)
	}

	if err := scanner.Err(); err != nil {
		t.failAllPending(fmt.Errorf("mcp stdio transport: read stdout: %w", err))
		return
	}
	t.failAllPending(io.EOF)
}

func (t *StdioTransport) waitLoop() {
	err := t.cmd.Wait()
	t.mu.Lock()
	t.waitErr = err
	t.mu.Unlock()
	close(t.waitDone)

	if err != nil {
		t.failAllPending(fmt.Errorf("mcp stdio transport: process exit: %w", err))
	}
}

func (t *StdioTransport) dispatchResponse(response Response) {
	t.mu.Lock()
	ch := t.pending[response.ID]
	t.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- stdioResult{response: response}:
	default:
	}
}

func (t *StdioTransport) failAllPending(err error) {
	if err == nil {
		err = io.EOF
	}

	t.mu.Lock()
	if t.readErr == nil {
		t.readErr = err
	}
	pending := t.pending
	t.pending = make(map[int]chan stdioResult)
	t.mu.Unlock()

	for id, ch := range pending {
		delete(pending, id)
		select {
		case ch <- stdioResult{err: err}:
		default:
		}
	}
}

func (t *StdioTransport) captureStderr(stderr io.Reader) {
	_, _ = io.Copy(stderrBufferWriter{transport: t}, stderr)
}

func (t *StdioTransport) withStderr(err error) error {
	if err == nil {
		return nil
	}
	stderr := t.stderrTail()
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w (stderr: %s)", err, stderr)
}

func (t *StdioTransport) stderrTail() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()

	text := strings.TrimSpace(t.stderrBuf.String())
	if text == "" {
		return ""
	}
	if len(text) <= stderrTailLimit {
		return text
	}
	return text[len(text)-stderrTailLimit:]
}

type stderrBufferWriter struct {
	transport *StdioTransport
}

func (w stderrBufferWriter) Write(p []byte) (int, error) {
	if w.transport == nil {
		return len(p), nil
	}
	w.transport.stderrMu.Lock()
	defer w.transport.stderrMu.Unlock()
	return w.transport.stderrBuf.Write(p)
}

// SSETransport sends JSON-RPC over HTTP POST and parses SSE responses.
type SSETransport struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
	nextID     int64
}

// NewSSETransport creates one HTTP/SSE transport.
func NewSSETransport(url string, headers map[string]string) (*SSETransport, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, errors.New("mcp sse transport: url is required")
	}

	return &SSETransport{
		endpoint:   url,
		headers:    cloneStringMap(headers),
		httpClient: &http.Client{Timeout: sseRequestTimeout},
	}, nil
}

// Send sends one JSON-RPC request over HTTP POST and returns matched response.
func (t *SSETransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return nil, errors.New("mcp sse transport: nil transport")
	}

	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("mcp sse transport: method is required")
	}

	id := int(atomic.AddInt64(&t.nextID, 1))
	request := Request{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("mcp sse transport: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("mcp sse transport: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for key, value := range t.headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		httpReq.Header.Set(key, value)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp sse transport: send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(readAllString(resp.Body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("mcp sse transport: status %d: %s", resp.StatusCode, message)
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSEResponse(resp.Body, id)
	}

	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("mcp sse transport: decode response: %w", err)
	}
	if rpcResp.ID != id {
		return nil, fmt.Errorf("mcp sse transport: unexpected response id %d (want %d)", rpcResp.ID, id)
	}
	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return cloneRawMessage(rpcResp.Result), nil
}

// Notify sends one JSON-RPC notification over HTTP POST.
func (t *SSETransport) Notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return errors.New("mcp sse transport: nil transport")
	}

	method = strings.TrimSpace(method)
	if method == "" {
		return errors.New("mcp sse transport: method is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mcp sse transport: notify: %w", err)
	}

	payload, err := json.Marshal(notificationRequest{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("mcp sse transport: marshal notification: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mcp sse transport: build notification request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	for key, value := range t.headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		httpReq.Header.Set(key, value)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mcp sse transport: send notification: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(readAllString(resp.Body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("mcp sse transport: notification status %d: %s", resp.StatusCode, message)
	}
	return nil
}

// Close closes idle HTTP connections.
func (t *SSETransport) Close() error {
	if t == nil || t.httpClient == nil || t.httpClient.Transport == nil {
		return nil
	}
	if closer, ok := t.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	return nil
}

func parseSSEResponse(body io.Reader, expectedID int) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), sseScannerMaxSize)

	dataLines := make([]string, 0, 4)
	flushData := func() (json.RawMessage, error, bool) {
		if len(dataLines) == 0 {
			return nil, nil, false
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			return nil, nil, false
		}

		var response Response
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			return nil, fmt.Errorf("mcp sse transport: decode stream payload: %w", err), true
		}
		if response.ID == 0 || response.ID != expectedID {
			// Ignore notifications and unrelated responses.
			return nil, nil, false
		}
		if response.Error != nil {
			return nil, response.Error, true
		}
		return cloneRawMessage(response.Result), nil, true
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result, err, matched := flushData()
			if matched || err != nil {
				return result, err
			}
			continue
		}
		if strings.HasPrefix(trimmed, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			dataLines = append(dataLines, payload)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp sse transport: read stream: %w", err)
	}

	result, err, matched := flushData()
	if matched || err != nil {
		return result, err
	}
	return nil, errors.New("mcp sse transport: response not found in stream")
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func readAllString(reader io.Reader) string {
	body, err := io.ReadAll(reader)
	if err != nil {
		return ""
	}
	return string(body)
}

func startCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil {
		return errors.New("mcp stdio transport: nil command")
	}
	if timeout <= 0 {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("mcp stdio transport: start process: %w", err)
		}
		return nil
	}

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- cmd.Start()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-startErrCh:
		if err != nil {
			return fmt.Errorf("mcp stdio transport: start process: %w", err)
		}
		return nil
	case <-timer.C:
		go func() {
			if err := <-startErrCh; err == nil && cmd.Process != nil {
				_ = cmd.Process.Signal(os.Kill)
				_, _ = cmd.Process.Wait()
			}
		}()
		return fmt.Errorf("mcp stdio transport: start process timeout after %s", timeout)
	}
}

func buildProcessEnv(extra map[string]string) []string {
	base := cloneStrings(os.Environ())
	if len(extra) == 0 {
		return base
	}

	keys := make([]string, 0, len(extra))
	for key := range extra {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i := range keys {
		base = append(base, keys[i]+"="+extra[keys[i]])
	}
	return base
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
