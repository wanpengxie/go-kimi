package moonshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestNewMoonshotClientDefaultsAndFromConfig(t *testing.T) {
	t.Parallel()

	client := NewMoonshotClient(" api-key ", "", " kimi-k2 ")
	if client.apiKey != "api-key" {
		t.Fatalf("apiKey = %q, want %q", client.apiKey, "api-key")
	}
	if client.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", client.baseURL, defaultBaseURL)
	}
	if client.model != "kimi-k2" {
		t.Fatalf("model = %q, want %q", client.model, "kimi-k2")
	}
	if client.httpClient == nil {
		t.Fatal("httpClient should not be nil")
	}
	if client.httpClient.Timeout != defaultRequestTO {
		t.Fatalf("http timeout = %v, want %v", client.httpClient.Timeout, defaultRequestTO)
	}

	cfgProvider := config.LLMProvider{
		APIKey:  "k2",
		BaseURL: "https://example.com/v1",
	}
	cfgModel := config.LLMModel{Name: "kimi-k2"}
	fromConfig := NewMoonshotClientFromConfig(cfgProvider, cfgModel)
	if fromConfig.apiKey != "k2" || fromConfig.baseURL != "https://example.com/v1" || fromConfig.model != "kimi-k2" {
		t.Fatalf("NewMoonshotClientFromConfig() mismatch: %#v", fromConfig)
	}
}

func TestMoonshotClientChat(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer test-key")
		}

		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "kimi-k2" {
			t.Fatalf("request model = %q, want %q", req.Model, "kimi-k2")
		}
		if req.ReasoningEffort != "high" {
			t.Fatalf("reasoning_effort = %q, want %q", req.ReasoningEffort, "high")
		}
		if len(req.Messages) != 1 {
			t.Fatalf("message count = %d, want 1", len(req.Messages))
		}
		if req.Messages[0].Role != "user" {
			t.Fatalf("message role = %q, want %q", req.Messages[0].Role, "user")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "pong",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "search", "arguments": "{\"q\":\"moonshot\"}"}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {
				"prompt_tokens": 11,
				"completion_tokens": 7,
				"total_tokens": 18
			}
		}`))
	}))
	defer server.Close()

	client := NewMoonshotClient("test-key", server.URL+"/v1", "kimi-k2")
	provider, ok := client.WithThinking("high").(*MoonshotClient)
	if !ok {
		t.Fatal("WithThinking() should return *MoonshotClient")
	}

	resp, err := provider.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{
			Role: "user",
			Content: types.ContentParts{
				types.TextPart{Text: "ping"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("StopReason = %q, want %q", resp.StopReason, "tool_calls")
	}
	if resp.Usage.TotalTokens != 18 {
		t.Fatalf("total_tokens = %d, want 18", resp.Usage.TotalTokens)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content parts = %d, want 1", len(resp.Content))
	}
	textPart, ok := resp.Content[0].(types.TextPart)
	if !ok || textPart.Text != "pong" {
		t.Fatalf("content[0] = %#v, want text part pong", resp.Content[0])
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Fatalf("tool name = %q, want %q", resp.ToolCalls[0].Name, "search")
	}
	arguments, ok := resp.ToolCalls[0].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("tool args type = %T, want map[string]any", resp.ToolCalls[0].Arguments)
	}
	if got, _ := arguments["q"].(string); got != "moonshot" {
		t.Fatalf("tool args q = %q, want %q", got, "moonshot")
	}
}

func TestMoonshotClientChatRequestEncodesAssistantToolCallsAndSkipsThink(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 3 {
			t.Fatalf("message count = %d, want 3", len(req.Messages))
		}

		assistant := req.Messages[1]
		if assistant.Role != "assistant" {
			t.Fatalf("assistant role = %q, want assistant", assistant.Role)
		}
		if len(assistant.ToolCalls) != 1 {
			t.Fatalf("assistant tool call count = %d, want 1", len(assistant.ToolCalls))
		}
		call := assistant.ToolCalls[0]
		if call.ID != "call-1" || call.Type != "function" || call.Function.Name != "search" {
			t.Fatalf("assistant tool call = %#v, want id=call-1 type=function name=search", call)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			t.Fatalf("decode tool arguments %q: %v", call.Function.Arguments, err)
		}
		if got, _ := args["q"].(string); got != "moonshot" {
			t.Fatalf("tool arguments q = %q, want moonshot", got)
		}

		assistantContentJSON, err := json.Marshal(assistant.Content)
		if err != nil {
			t.Fatalf("marshal assistant content: %v", err)
		}
		if strings.Contains(string(assistantContentJSON), "\"think\"") {
			t.Fatalf("assistant content should not include think part: %s", assistantContentJSON)
		}
		if !strings.Contains(string(assistantContentJSON), "\"text\":\"working\"") {
			t.Fatalf("assistant content should retain text part: %s", assistantContentJSON)
		}
		if assistant.ReasoningContent != "internal thought" {
			t.Fatalf("assistant reasoning_content = %q, want %q", assistant.ReasoningContent, "internal thought")
		}

		tool := req.Messages[2]
		if tool.Role != "tool" || tool.ToolCallID != "call-1" {
			t.Fatalf("tool message = %#v, want role=tool tool_call_id=call-1", tool)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewMoonshotClient("test-key", server.URL+"/v1", "kimi-k2")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{
				Role: "user",
				Content: types.ContentParts{
					types.TextPart{Text: "hello"},
				},
			},
			{
				Role: "assistant",
				Content: types.ContentParts{
					types.ThinkPart{Think: "internal thought"},
					types.TextPart{Text: "working"},
				},
				ToolCalls: []types.ToolCall{
					{
						ID:   "call-1",
						Name: "search",
						Arguments: map[string]any{
							"q": "moonshot",
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call-1",
				Content: types.ContentParts{
					types.TextPart{Text: "tool output"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
}

func TestMoonshotClientChatStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want %q", got, "text/event-stream")
		}

		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !req.Stream {
			t.Fatal("stream flag should be true")
		}
		if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Fatalf("stream_options = %#v, want include_usage=true", req.StreamOptions)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support flush")
		}

		chunks := []string{
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"{\"q\":\"hel"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"lo\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewMoonshotClient("stream-key", server.URL+"/v1", "kimi-k2")
	stream, err := client.ChatStream(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{
			Role: "user",
			Content: types.ContentParts{
				types.TextPart{Text: "hello"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	events := make([]llm.ChatEvent, 0, 4)
	for event := range stream {
		events = append(events, event)
	}
	if len(events) < 4 {
		t.Fatalf("event count = %d, want >= 4", len(events))
	}

	text1, ok := events[0].Delta.(types.TextPart)
	if !ok || text1.Text != "Hel" {
		t.Fatalf("event[0].Delta = %#v, want text Hel", events[0].Delta)
	}
	text2, ok := events[1].Delta.(types.TextPart)
	if !ok || text2.Text != "lo" {
		t.Fatalf("event[1].Delta = %#v, want text lo", events[1].Delta)
	}

	if events[2].ToolCall == nil {
		t.Fatalf("event[2].ToolCall = nil, want tool call")
	}
	if events[2].ToolCall.Name != "search" {
		t.Fatalf("event[2].ToolCall.Name = %q, want %q", events[2].ToolCall.Name, "search")
	}
	args, ok := events[2].ToolCall.Arguments.(map[string]any)
	if !ok {
		t.Fatalf("tool args type = %T, want map[string]any", events[2].ToolCall.Arguments)
	}
	if q, _ := args["q"].(string); q != "hello" {
		t.Fatalf("tool args q = %q, want %q", q, "hello")
	}

	last := events[len(events)-1]
	if !last.Done {
		t.Fatal("last event should be Done")
	}
	if last.Usage == nil || last.Usage.TotalTokens != 3 {
		t.Fatalf("last usage = %#v, want total_tokens=3", last.Usage)
	}
}

func TestMoonshotClientRetryOnRetryableStatus(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		switch attempt {
		case 1:
			http.Error(w, "temporary", http.StatusInternalServerError)
		case 2:
			http.Error(w, "rate limit", http.StatusTooManyRequests)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
		}
	}))
	defer server.Close()

	client := NewMoonshotClient("retry-key", server.URL+"/v1", "kimi-k2")
	client.initialBackoff = time.Millisecond

	resp, err := client.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: types.ContentParts{types.TextPart{Text: "ping"}}}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil {
		t.Fatal("Chat() response should not be nil")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempt count = %d, want 3", got)
	}
}

func TestMoonshotClientNoRetryOnClientError(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewMoonshotClient("bad-request-key", server.URL+"/v1", "kimi-k2")
	client.initialBackoff = time.Millisecond

	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: types.ContentParts{types.TextPart{Text: "ping"}}}},
	})
	if err == nil {
		t.Fatal("Chat() expected error for 400 response")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempt count = %d, want 1", got)
	}
}

func TestMoonshotClientRetryOnNetworkError(t *testing.T) {
	t.Parallel()

	var serverAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&serverAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewMoonshotClient("network-key", server.URL+"/v1", "kimi-k2")
	client.initialBackoff = time.Millisecond
	client.httpClient.Transport = &flakyTransport{
		remainingFailures: 1,
		next:              http.DefaultTransport,
	}

	resp, err := client.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: types.ContentParts{types.TextPart{Text: "ping"}}}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil {
		t.Fatal("Chat() response should not be nil")
	}
	if got := atomic.LoadInt32(&serverAttempts); got != 1 {
		t.Fatalf("server attempts = %d, want 1", got)
	}
}

type flakyTransport struct {
	remainingFailures int32
	next              http.RoundTripper
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if atomic.AddInt32(&t.remainingFailures, -1) >= 0 {
		return nil, errors.New("temporary network failure")
	}
	return t.next.RoundTrip(req)
}
