package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestNewAnthropicClientDefaultsAndFromConfig(t *testing.T) {
	t.Parallel()

	client := NewAnthropicClient(" api-key ", "", " claude-3-5-sonnet ")
	if client.apiKey != "api-key" {
		t.Fatalf("apiKey = %q, want %q", client.apiKey, "api-key")
	}
	if client.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", client.baseURL, defaultBaseURL)
	}
	if client.model != "claude-3-5-sonnet" {
		t.Fatalf("model = %q, want %q", client.model, "claude-3-5-sonnet")
	}
	if client.httpClient == nil {
		t.Fatal("httpClient should not be nil")
	}
	if client.httpClient.Timeout != defaultRequestTO {
		t.Fatalf("http timeout = %v, want %v", client.httpClient.Timeout, defaultRequestTO)
	}

	cfgProvider := config.LLMProvider{
		APIKey:  "anthropic-key",
		BaseURL: "https://example.com",
	}
	cfgModel := config.LLMModel{Name: "claude-3-5-haiku"}
	fromConfig := NewAnthropicClientFromConfig(cfgProvider, cfgModel)
	if fromConfig.apiKey != "anthropic-key" || fromConfig.baseURL != "https://example.com" || fromConfig.model != "claude-3-5-haiku" {
		t.Fatalf("NewAnthropicClientFromConfig() mismatch: %#v", fromConfig)
	}
}

func TestAnthropicClientWithModelClonesProvider(t *testing.T) {
	t.Parallel()

	client := NewAnthropicClient("test-key", "https://example.com", "claude-3-5-haiku")
	overridden, ok := client.WithModel(" claude-3-5-sonnet ").(*AnthropicClient)
	if !ok {
		t.Fatal("WithModel() should return *AnthropicClient")
	}
	if overridden == client {
		t.Fatal("WithModel() returned same pointer, want clone")
	}
	if overridden.model != "claude-3-5-sonnet" {
		t.Fatalf("overridden model = %q, want %q", overridden.model, "claude-3-5-sonnet")
	}
	if client.model != "claude-3-5-haiku" {
		t.Fatalf("original model = %q, want %q", client.model, "claude-3-5-haiku")
	}
}

func TestAnthropicFactoryRegistration(t *testing.T) {
	t.Parallel()

	provider, err := llm.NewProvider(llm.ProviderConfig{
		Type:    "anthropic",
		APIKey:  "test-key",
		BaseURL: "https://example.com",
		Model:   "claude-3-5-haiku",
	})
	if err != nil {
		t.Fatalf("NewProvider(anthropic) error = %v", err)
	}
	typed, ok := provider.(*AnthropicClient)
	if !ok {
		t.Fatalf("provider type = %T, want *AnthropicClient", provider)
	}
	if typed.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want %q", typed.apiKey, "test-key")
	}
	if typed.baseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want %q", typed.baseURL, "https://example.com")
	}
	if typed.model != "claude-3-5-haiku" {
		t.Fatalf("model = %q, want %q", typed.model, "claude-3-5-haiku")
	}
}

func TestAnthropicClientChat(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("anthropic-version"); got != defaultAnthropicVersion {
			t.Fatalf("anthropic-version = %q, want %q", got, defaultAnthropicVersion)
		}

		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "claude-3-5-haiku" {
			t.Fatalf("request model = %q, want %q", req.Model, "claude-3-5-haiku")
		}
		if req.System != "you are helpful" {
			t.Fatalf("system = %q, want %q", req.System, "you are helpful")
		}
		if req.Thinking == nil || req.Thinking.Type != "enabled" {
			t.Fatalf("thinking = %#v, want enabled", req.Thinking)
		}
		if req.MaxTokens != defaultMaxTokens {
			t.Fatalf("max_tokens = %d, want %d", req.MaxTokens, defaultMaxTokens)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("messages = %#v, want one user message", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [
				{"type":"thinking","thinking":"plan"},
				{"type":"text","text":"pong"},
				{"type":"tool_use","id":"toolu_1","name":"search","input":{"q":"anthropic"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":11,"output_tokens":7}
		}`))
	}))
	defer server.Close()

	client := NewAnthropicClient("test-key", server.URL, "claude-3-5-haiku")
	provider, ok := client.WithThinking(llm.ThinkingHigh).(*AnthropicClient)
	if !ok {
		t.Fatal("WithThinking() should return *AnthropicClient")
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
	if len(resp.Content) != 2 {
		t.Fatalf("content parts = %d, want 2", len(resp.Content))
	}
	if thinking, ok := resp.Content[0].(types.ThinkPart); !ok || thinking.Think != "plan" {
		t.Fatalf("content[0] = %#v, want think plan", resp.Content[0])
	}
	if text, ok := resp.Content[1].(types.TextPart); !ok || text.Text != "pong" {
		t.Fatalf("content[1] = %#v, want text pong", resp.Content[1])
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Fatalf("tool name = %q, want %q", resp.ToolCalls[0].Name, "search")
	}
	args, ok := resp.ToolCalls[0].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("tool args type = %T, want map[string]any", resp.ToolCalls[0].Arguments)
	}
	if got, _ := args["q"].(string); got != "anthropic" {
		t.Fatalf("tool args q = %q, want %q", got, "anthropic")
	}
}

func TestAnthropicClientChatRequestEncodesSystemToolResultAndAssistantToolUse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.System != "You are a helper." {
			t.Fatalf("system = %q, want %q", req.System, "You are a helper.")
		}
		if len(req.Messages) != 3 {
			t.Fatalf("message count = %d, want 3", len(req.Messages))
		}

		assistant := req.Messages[1]
		if assistant.Role != "assistant" {
			t.Fatalf("assistant role = %q, want assistant", assistant.Role)
		}
		// 3 blocks: thinking + text + tool_use. The thinking block MUST
		// round-trip back to the API — DeepSeek's anthropic-compat
		// endpoint and upstream Anthropic both reject requests where a
		// historical assistant turn loses its thinking block.
		if len(assistant.Content) != 3 {
			t.Fatalf("assistant content blocks = %d, want 3 (thinking+text+tool_use): %#v", len(assistant.Content), assistant.Content)
		}
		if assistant.Content[0].Type != "thinking" || assistant.Content[0].Thinking != "internal thought" {
			t.Fatalf("assistant content[0] = %#v, want thinking 'internal thought'", assistant.Content[0])
		}
		if assistant.Content[1].Type != "text" || assistant.Content[1].Text != "working" {
			t.Fatalf("assistant content[1] = %#v, want text working", assistant.Content[1])
		}
		if assistant.Content[2].Type != "tool_use" || assistant.Content[2].ID != "call-1" || assistant.Content[2].Name != "search" {
			t.Fatalf("assistant content[2] = %#v, want tool_use call-1/search", assistant.Content[2])
		}

		toolResult := req.Messages[2]
		if toolResult.Role != "user" {
			t.Fatalf("tool result role = %q, want user", toolResult.Role)
		}
		if len(toolResult.Content) != 1 {
			t.Fatalf("tool result content blocks = %d, want 1", len(toolResult.Content))
		}
		block := toolResult.Content[0]
		if block.Type != "tool_result" || block.ToolUseID != "call-1" {
			t.Fatalf("tool result block = %#v, want tool_result call-1", block)
		}
		if got, ok := block.Content.(string); !ok || got != "tool output" {
			t.Fatalf("tool result content = %#v, want %q", block.Content, "tool output")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	client := NewAnthropicClient("test-key", server.URL, "claude-3-5-haiku")
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
							"q": "anthropic",
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

func TestAnthropicClientChatStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want %q", got, "text/event-stream")
		}

		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !req.Stream {
			t.Fatal("stream flag should be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support flush")
		}

		frames := []string{
			"event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n",
			"event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
			"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n",
			"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n",
			"event: content_block_stop\ndata: {\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"thinking\"}}\n\n",
			"event: content_block_delta\ndata: {\"index\":1,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n",
			"event: content_block_stop\ndata: {\"index\":1}\n\n",
			"event: content_block_start\ndata: {\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"search\"}}\n\n",
			"event: content_block_delta\ndata: {\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"hel\"}}\n\n",
			"event: content_block_delta\ndata: {\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"lo\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"index\":2}\n\n",
			"event: message_delta\ndata: {\"usage\":{\"output_tokens\":2},\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
			"event: message_stop\ndata: {}\n\n",
		}
		for _, frame := range frames {
			_, _ = fmt.Fprint(w, frame)
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewAnthropicClient("stream-key", server.URL, "claude-3-5-haiku")
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

	events := make([]llm.ChatEvent, 0, 6)
	for event := range stream {
		events = append(events, event)
	}
	if len(events) < 5 {
		t.Fatalf("event count = %d, want >= 5", len(events))
	}

	text1, ok := events[0].Delta.(types.TextPart)
	if !ok || text1.Text != "Hel" {
		t.Fatalf("event[0].Delta = %#v, want text Hel", events[0].Delta)
	}
	text2, ok := events[1].Delta.(types.TextPart)
	if !ok || text2.Text != "lo" {
		t.Fatalf("event[1].Delta = %#v, want text lo", events[1].Delta)
	}
	thinking, ok := events[2].Delta.(types.ThinkPart)
	if !ok || thinking.Think != "plan" {
		t.Fatalf("event[2].Delta = %#v, want think plan", events[2].Delta)
	}

	if events[3].ToolCall == nil {
		t.Fatalf("event[3].ToolCall = nil, want tool call")
	}
	if events[3].ToolCall.Name != "search" {
		t.Fatalf("event[3].ToolCall.Name = %q, want %q", events[3].ToolCall.Name, "search")
	}
	args, ok := events[3].ToolCall.Arguments.(map[string]any)
	if !ok {
		t.Fatalf("tool args type = %T, want map[string]any", events[3].ToolCall.Arguments)
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

func TestAnthropicClientRetryOnRetryableStatus(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
		}
	}))
	defer server.Close()

	client := NewAnthropicClient("retry-key", server.URL, "claude-3-5-haiku")
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

func TestAnthropicClientNoRetryOnClientError(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewAnthropicClient("bad-request-key", server.URL, "claude-3-5-haiku")
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

func TestAnthropicClientRetryOnNetworkError(t *testing.T) {
	t.Parallel()

	var serverAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&serverAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	client := NewAnthropicClient("network-key", server.URL, "claude-3-5-haiku")
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

// TestMessageContentBlockThinkingAlwaysEmitsThinkingField guards the
// long-conversation 400 regression: DeepSeek's anthropic-compat endpoint
// (and upstream Anthropic) reject requests where a historical thinking
// block in `messages[N].content` lacks the `thinking` field. The wire
// schema requires the field to be present even when its value is the
// empty string, so messageContentBlock MUST always emit it for thinking
// blocks regardless of `omitempty`.
func TestMessageContentBlockThinkingAlwaysEmitsThinkingField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		block messageContentBlock
	}{
		{
			name: "non-empty think + signature",
			block: messageContentBlock{
				Type:      "thinking",
				Thinking:  "plan",
				Signature: "sig-xyz",
			},
		},
		{
			name: "empty think + non-empty signature",
			block: messageContentBlock{
				Type:      "thinking",
				Thinking:  "",
				Signature: "sig-xyz",
			},
		},
		{
			name: "non-empty think + empty signature",
			block: messageContentBlock{
				Type:      "thinking",
				Thinking:  "plan",
				Signature: "",
			},
		},
		{
			name: "both empty (degenerate but allowed)",
			block: messageContentBlock{
				Type:      "thinking",
				Thinking:  "",
				Signature: "",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tc.block)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var generic map[string]any
			if err := json.Unmarshal(data, &generic); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if generic["type"] != "thinking" {
				t.Fatalf("type = %v, want thinking; payload=%s", generic["type"], string(data))
			}
			if _, ok := generic["thinking"]; !ok {
				t.Fatalf("thinking field missing in payload=%s", string(data))
			}
			if _, ok := generic["signature"]; !ok {
				t.Fatalf("signature field missing in payload=%s", string(data))
			}
			if generic["thinking"] != tc.block.Thinking {
				t.Fatalf("thinking = %v, want %q", generic["thinking"], tc.block.Thinking)
			}
			if generic["signature"] != tc.block.Signature {
				t.Fatalf("signature = %v, want %q", generic["signature"], tc.block.Signature)
			}
			// Tool-use / tool_result fields must NOT leak into thinking blocks.
			for _, forbidden := range []string{"id", "name", "input", "tool_use_id", "content", "is_error", "text"} {
				if _, ok := generic[forbidden]; ok {
					t.Fatalf("thinking block leaked field %q in payload=%s", forbidden, string(data))
				}
			}
		})
	}
}

// TestMessageContentBlockNonThinkingPreservesOmitempty makes sure the
// custom marshaler only changes thinking-block behavior; for text / tool_use
// blocks the omitempty semantics on Thinking/Signature MUST still drop empty
// values so we don't pollute every block with `"thinking":""`.
func TestMessageContentBlockNonThinkingPreservesOmitempty(t *testing.T) {
	t.Parallel()

	textBlock := messageContentBlock{Type: "text", Text: "hello"}
	data, err := json.Marshal(textBlock)
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("unmarshal text: %v", err)
	}
	if _, ok := generic["thinking"]; ok {
		t.Fatalf("text block leaked thinking field: %s", string(data))
	}
	if _, ok := generic["signature"]; ok {
		t.Fatalf("text block leaked signature field: %s", string(data))
	}
	if generic["text"] != "hello" {
		t.Fatalf("text = %v, want hello", generic["text"])
	}

	toolBlock := messageContentBlock{
		Type:  "tool_use",
		ID:    "call-1",
		Name:  "search",
		Input: map[string]any{"q": "x"},
	}
	data, err = json.Marshal(toolBlock)
	if err != nil {
		t.Fatalf("marshal tool_use: %v", err)
	}
	generic = nil
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("unmarshal tool_use: %v", err)
	}
	if _, ok := generic["thinking"]; ok {
		t.Fatalf("tool_use block leaked thinking field: %s", string(data))
	}
	if _, ok := generic["signature"]; ok {
		t.Fatalf("tool_use block leaked signature field: %s", string(data))
	}
}

// TestAnthropicClientChatLongConversationThinkingFieldsPreserved simulates
// the multi-step long-conversation regression at the request-build level:
// 10+ assistant turns each containing a ThinkPart (some with empty Think
// text and only a Signature, simulating partial reconstruction) get
// serialized into the request body and the wire payload MUST contain a
// `thinking` field on every thinking block — DeepSeek's anthropic-compat
// endpoint hard-rejects requests where any historical thinking block has
// dropped that field.
func TestAnthropicClientChatLongConversationThinkingFieldsPreserved(t *testing.T) {
	t.Parallel()

	var capturedRaw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capturedRaw = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	// Build 12 assistant messages alternating ThinkPart shapes:
	//   - even index: full Think + Signature (normal streaming output)
	//   - odd  index: empty Think but non-empty Signature (the bug case
	//     — happens when reconstruction loses the thinking text but
	//     keeps the signature, or when the model emitted only signature)
	messages := []llm.Message{
		{Role: "system", Content: types.ContentParts{types.TextPart{Text: "be helpful"}}},
		{Role: "user", Content: types.ContentParts{types.TextPart{Text: "go"}}},
	}
	for i := 0; i < 12; i++ {
		var think string
		if i%2 == 0 {
			think = fmt.Sprintf("thought-%d", i)
		}
		messages = append(messages, llm.Message{
			Role: "assistant",
			Content: types.ContentParts{
				types.ThinkPart{Think: think, Signature: fmt.Sprintf("sig-%d", i)},
				types.TextPart{Text: fmt.Sprintf("step-%d", i)},
			},
		})
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: types.ContentParts{types.TextPart{Text: fmt.Sprintf("next-%d", i)}},
		})
	}

	client := NewAnthropicClient("test-key", server.URL, "claude-3-5-haiku")
	if _, err := client.Chat(context.Background(), llm.ChatRequest{Messages: messages}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Decode the captured wire payload as a generic map so we can verify
	// raw field presence (default struct decoding would silently fill in
	// zero-valued strings and hide the bug).
	var raw struct {
		Messages []struct {
			Role    string                   `json:"role"`
			Content []map[string]interface{} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(capturedRaw, &raw); err != nil {
		t.Fatalf("decode raw payload: %v\npayload=%s", err, string(capturedRaw))
	}

	thinkingBlockCount := 0
	for mi, m := range raw.Messages {
		for ci, block := range m.Content {
			if block["type"] != "thinking" {
				continue
			}
			thinkingBlockCount++
			if _, ok := block["thinking"]; !ok {
				t.Fatalf("messages[%d].content[%d]: missing field `thinking`; block=%v", mi, ci, block)
			}
			if _, ok := block["signature"]; !ok {
				t.Fatalf("messages[%d].content[%d]: missing field `signature`; block=%v", mi, ci, block)
			}
		}
	}
	if thinkingBlockCount != 12 {
		t.Fatalf("thinking blocks in payload = %d, want 12", thinkingBlockCount)
	}
}


// TestEncodeRegularContentCollapsesChunkedTextParts verifies that 343-
// element TextPart histories (one per streamed token, courtesy of
// providers that one-shot the chunked stream into Output) are collapsed
// to a single text content_block before being sent back to the provider.
// This is the upstream half of the chunked-content self-perpetuation bug;
// without collapse, the next turn's request mimics the chunked format and
// the model in-context-learns the pattern.
func TestEncodeRegularContentCollapsesChunkedTextParts(t *testing.T) {
	t.Parallel()

	parts := types.ContentParts{}
	for i := 0; i < 343; i++ {
		parts = append(parts, types.TextPart{Text: "tok"})
	}

	blocks := encodeRegularContent(parts)
	if len(blocks) != 1 {
		t.Fatalf("encoded block count = %d, want 1 collapsed text block", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Fatalf("block[0].Type = %q, want text", blocks[0].Type)
	}
	want := ""
	for i := 0; i < 343; i++ {
		want += "tok"
	}
	if blocks[0].Text != want {
		t.Fatalf("block[0].Text len = %d, want %d", len(blocks[0].Text), len(want))
	}
}

// TestEncodeRegularContentPreservesThinkingBoundaries — chunked text
// around thinking blocks must collapse on both sides without absorbing the
// thinking block. Thinking signature is preserved verbatim.
func TestEncodeRegularContentPreservesThinkingBoundaries(t *testing.T) {
	t.Parallel()

	parts := types.ContentParts{
		types.TextPart{Text: "Plan "},
		types.TextPart{Text: "the "},
		types.TextPart{Text: "task. "},
		types.ThinkPart{Think: "internal monologue", Signature: "deepseek-sig"},
		types.TextPart{Text: "Now "},
		types.TextPart{Text: "execute."},
	}

	blocks := encodeRegularContent(parts)
	if len(blocks) != 3 {
		t.Fatalf("block count = %d, want 3 (text, thinking, text); got %#v", len(blocks), blocks)
	}

	if blocks[0].Type != "text" || blocks[0].Text != "Plan the task. " {
		t.Fatalf("block[0] = %+v, want text{Plan the task. }", blocks[0])
	}
	if blocks[1].Type != "thinking" || blocks[1].Thinking != "internal monologue" || blocks[1].Signature != "deepseek-sig" {
		t.Fatalf("block[1] = %+v, want thinking{internal monologue, deepseek-sig}", blocks[1])
	}
	if blocks[2].Type != "text" || blocks[2].Text != "Now execute." {
		t.Fatalf("block[2] = %+v, want text{Now execute.}", blocks[2])
	}
}

// TestEncodeRegularContentDropsEmptyTextChunks — empty TextPart entries
// (e.g. provider sentinel tokens) must not produce empty content_block
// entries that some providers reject.
func TestEncodeRegularContentDropsEmptyTextChunks(t *testing.T) {
	t.Parallel()

	parts := types.ContentParts{
		types.TextPart{Text: ""},
		types.TextPart{Text: "real text"},
		types.TextPart{Text: ""},
	}

	blocks := encodeRegularContent(parts)
	if len(blocks) != 1 {
		t.Fatalf("block count = %d, want 1", len(blocks))
	}
	if blocks[0].Text != "real text" {
		t.Fatalf("block[0].Text = %q, want %q", blocks[0].Text, "real text")
	}
}
