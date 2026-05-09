package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestNewGeminiClientDefaultsAndFromConfig(t *testing.T) {
	t.Parallel()

	client := NewGeminiClient(" api-key ", "", " gemini-2.0-flash ")
	if client.apiKey != "api-key" {
		t.Fatalf("apiKey = %q, want %q", client.apiKey, "api-key")
	}
	if client.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", client.baseURL, defaultBaseURL)
	}
	if client.model != "gemini-2.0-flash" {
		t.Fatalf("model = %q, want %q", client.model, "gemini-2.0-flash")
	}
	if client.httpClient == nil {
		t.Fatal("httpClient should not be nil")
	}
	if client.httpClient.Timeout != defaultRequestTO {
		t.Fatalf("http timeout = %v, want %v", client.httpClient.Timeout, defaultRequestTO)
	}

	cfgProvider := config.LLMProvider{
		APIKey:  "gemini-key",
		BaseURL: "https://example.com/v1beta",
	}
	cfgModel := config.LLMModel{Name: "gemini-2.0-pro"}
	fromConfig := NewGeminiClientFromConfig(cfgProvider, cfgModel)
	if fromConfig.apiKey != "gemini-key" || fromConfig.baseURL != "https://example.com/v1beta" || fromConfig.model != "gemini-2.0-pro" {
		t.Fatalf("NewGeminiClientFromConfig() mismatch: %#v", fromConfig)
	}
}

func TestGeminiClientWithModelClonesProvider(t *testing.T) {
	t.Parallel()

	client := NewGeminiClient("test-key", "https://example.com/v1beta", "gemini-2.0-flash")
	overridden, ok := client.WithModel(" gemini-2.0-pro ").(*GeminiClient)
	if !ok {
		t.Fatal("WithModel() should return *GeminiClient")
	}
	if overridden == client {
		t.Fatal("WithModel() returned same pointer, want clone")
	}
	if overridden.model != "gemini-2.0-pro" {
		t.Fatalf("overridden model = %q, want %q", overridden.model, "gemini-2.0-pro")
	}
	if client.model != "gemini-2.0-flash" {
		t.Fatalf("original model = %q, want %q", client.model, "gemini-2.0-flash")
	}
}

func TestGeminiFactoryRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  string
	}{
		{name: "gemini", typ: "gemini"},
		{name: "google alias", typ: "google"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, err := llm.NewProvider(llm.ProviderConfig{
				Type:    tc.typ,
				APIKey:  "test-key",
				BaseURL: "https://example.com/v1beta",
				Model:   "gemini-2.0-pro",
			})
			if err != nil {
				t.Fatalf("NewProvider(%q) error = %v", tc.typ, err)
			}
			typed, ok := provider.(*GeminiClient)
			if !ok {
				t.Fatalf("provider type = %T, want *GeminiClient", provider)
			}
			if typed.apiKey != "test-key" {
				t.Fatalf("apiKey = %q, want %q", typed.apiKey, "test-key")
			}
			if typed.baseURL != "https://example.com/v1beta" {
				t.Fatalf("baseURL = %q, want %q", typed.baseURL, "https://example.com/v1beta")
			}
			if typed.model != "gemini-2.0-pro" {
				t.Fatalf("model = %q, want %q", typed.model, "gemini-2.0-pro")
			}
		})
	}
}

func TestGeminiClientChat(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1beta/models/gemini-2.0-flash:generateContent" {
			t.Fatalf("path = %s, want /v1beta/models/gemini-2.0-flash:generateContent", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Fatalf("x-goog-api-key = %q, want %q", got, "test-key")
		}

		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.SystemInstruction == nil || len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != "you are helpful" {
			t.Fatalf("systemInstruction = %#v, want text you are helpful", req.SystemInstruction)
		}
		if len(req.Contents) != 1 || req.Contents[0].Role != "user" {
			t.Fatalf("contents = %#v, want one user content", req.Contents)
		}
		if req.GenerationConfig == nil || req.GenerationConfig.ThinkingConfig == nil {
			t.Fatalf("generationConfig = %#v, want thinking config", req.GenerationConfig)
		}
		if req.GenerationConfig.ThinkingConfig.ThinkingBudget != thinkingBudgetHigh {
			t.Fatalf("thinking budget = %d, want %d", req.GenerationConfig.ThinkingConfig.ThinkingBudget, thinkingBudgetHigh)
		}
		if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("tools = %#v, want one function declaration", req.Tools)
		}
		if req.Tools[0].FunctionDeclarations[0].Name != "search" {
			t.Fatalf("function name = %q, want %q", req.Tools[0].FunctionDeclarations[0].Name, "search")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"index": 0,
				"content": {
					"role": "model",
					"parts": [
						{"text": "pong"},
						{"functionCall": {"id": "call_1", "name": "search", "args": {"q": "gemini"}}}
					]
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 11,
				"candidatesTokenCount": 7,
				"totalTokenCount": 18
			}
		}`))
	}))
	defer server.Close()

	client := NewGeminiClient("test-key", server.URL, "gemini-2.0-flash")
	provider, ok := client.WithThinking(llm.ThinkingHigh).(*GeminiClient)
	if !ok {
		t.Fatal("WithThinking() should return *GeminiClient")
	}

	resp, err := provider.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{
				Role: "system",
				Content: types.ContentParts{
					types.TextPart{Text: "you are helpful"},
				},
			},
			{
				Role: "user",
				Content: types.ContentParts{
					types.TextPart{Text: "ping"},
				},
			},
		},
		Tools: []llm.ToolDefinition{
			{
				Name:        "search",
				Description: "search web",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
		MaxTokens: 4096,
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
	if got, _ := arguments["q"].(string); got != "gemini" {
		t.Fatalf("tool args q = %q, want %q", got, "gemini")
	}
}

func TestGeminiClientChatRequestEncodesSystemToolResultAndAssistantToolCall(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.SystemInstruction == nil || len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != "You are a helper." {
			t.Fatalf("systemInstruction = %#v, want text You are a helper.", req.SystemInstruction)
		}
		if len(req.Contents) != 3 {
			t.Fatalf("content count = %d, want 3", len(req.Contents))
		}

		assistant := req.Contents[1]
		if assistant.Role != "model" {
			t.Fatalf("assistant role = %q, want model", assistant.Role)
		}
		if len(assistant.Parts) != 2 {
			t.Fatalf("assistant parts = %d, want 2", len(assistant.Parts))
		}
		if assistant.Parts[0].Text != "working" {
			t.Fatalf("assistant part[0] = %#v, want text working", assistant.Parts[0])
		}
		if assistant.Parts[1].FunctionCall == nil {
			t.Fatalf("assistant part[1] = %#v, want function call", assistant.Parts[1])
		}
		if assistant.Parts[1].FunctionCall.ID != "call-1" || assistant.Parts[1].FunctionCall.Name != "search" {
			t.Fatalf("assistant function call = %#v, want id=call-1 name=search", assistant.Parts[1].FunctionCall)
		}

		toolResult := req.Contents[2]
		if toolResult.Role != "user" {
			t.Fatalf("tool result role = %q, want user", toolResult.Role)
		}
		if len(toolResult.Parts) != 1 || toolResult.Parts[0].FunctionResponse == nil {
			t.Fatalf("tool result part = %#v, want function response", toolResult.Parts)
		}
		if toolResult.Parts[0].FunctionResponse.Name != "search" {
			t.Fatalf("tool result function name = %q, want %q", toolResult.Parts[0].FunctionResponse.Name, "search")
		}
		responseBody, ok := toolResult.Parts[0].FunctionResponse.Response.(map[string]any)
		if !ok {
			t.Fatalf("tool result response type = %T, want map[string]any", toolResult.Parts[0].FunctionResponse.Response)
		}
		if got, _ := responseBody["content"].(string); got != "tool output" {
			t.Fatalf("tool result response content = %q, want %q", got, "tool output")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	client := NewGeminiClient("test-key", server.URL, "gemini-2.0-flash")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{
				Role: "system",
				Content: types.ContentParts{
					types.TextPart{Text: "You are a helper."},
				},
			},
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
							"q": "gemini",
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

func TestGeminiClientChatStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.0-flash:streamGenerateContent" {
			t.Fatalf("path = %s, want /v1beta/models/gemini-2.0-flash:streamGenerateContent", r.URL.Path)
		}
		if got := r.URL.Query().Get("alt"); got != "sse" {
			t.Fatalf("alt query = %q, want %q", got, "sse")
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want %q", got, "text/event-stream")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support flush")
		}

		frames := []string{
			`data: {"candidates":[{"index":0,"content":{"parts":[{"text":"Hel"}]}}]}`,
			`data: {"candidates":[{"index":0,"content":{"parts":[{"text":"lo"}]}}]}`,
			`data: {"candidates":[{"index":0,"content":{"parts":[{"functionCall":{"id":"call_1","name":"search","args":{"q":"gemini"}}}]}}]}`,
			`data: {"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`,
			`data: [DONE]`,
		}
		for _, frame := range frames {
			_, _ = fmt.Fprint(w, frame+"\n\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewGeminiClient("stream-key", server.URL, "gemini-2.0-flash")
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
	if q, _ := args["q"].(string); q != "gemini" {
		t.Fatalf("tool args q = %q, want %q", q, "gemini")
	}

	last := events[len(events)-1]
	if !last.Done {
		t.Fatal("last event should be Done")
	}
	if last.Usage == nil || last.Usage.TotalTokens != 3 {
		t.Fatalf("last usage = %#v, want total_tokens=3", last.Usage)
	}
}

func TestGeminiClientRetryOnRetryableStatus(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
		}
	}))
	defer server.Close()

	client := NewGeminiClient("retry-key", server.URL, "gemini-2.0-flash")
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

func TestGeminiClientNoRetryOnClientError(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewGeminiClient("bad-request-key", server.URL, "gemini-2.0-flash")
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

func TestGeminiClientRetryOnNetworkError(t *testing.T) {
	t.Parallel()

	var serverAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&serverAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	client := NewGeminiClient("network-key", server.URL, "gemini-2.0-flash")
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
		return nil, retryableTransportError{msg: "temporary network failure"}
	}
	return t.next.RoundTrip(req)
}

type retryableTransportError struct {
	msg string
}

func (e retryableTransportError) Error() string {
	return e.msg
}

func (e retryableTransportError) Timeout() bool {
	return false
}

func (e retryableTransportError) Temporary() bool {
	return true
}
