//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	anthropicllm "github.com/wanpengxie/go-kimi/pkg/kimi/llm/anthropic"
	geminillm "github.com/wanpengxie/go-kimi/pkg/kimi/llm/gemini"
	moonshotllm "github.com/wanpengxie/go-kimi/pkg/kimi/llm/moonshot"
	openaillm "github.com/wanpengxie/go-kimi/pkg/kimi/llm/openai"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func TestScriptedProviderFactory(t *testing.T) {
	cases := []struct {
		name           string
		cfg            llm.ProviderConfig
		assertProvider func(t *testing.T, provider llm.ChatProvider)
	}{
		{
			name: "moonshot",
			cfg: llm.ProviderConfig{
				Type:    "moonshot",
				APIKey:  "moonshot-key",
				BaseURL: "https://example.moonshot/v1",
				Model:   "kimi-k2",
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*moonshotllm.MoonshotClient)
				if !ok {
					t.Fatalf("provider type = %T, want *MoonshotClient", provider)
				}
				if got := typed.ModelName(); got != "kimi-k2" {
					t.Fatalf("ModelName() = %q, want %q", got, "kimi-k2")
				}
			},
		},
		{
			name: "openai",
			cfg: llm.ProviderConfig{
				Type:    "openai",
				APIKey:  "openai-key",
				BaseURL: "https://example.openai/v1",
				Model:   "gpt-4o-mini",
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*openaillm.OpenAIClient)
				if !ok {
					t.Fatalf("provider type = %T, want *OpenAIClient", provider)
				}
				if got := typed.ModelName(); got != "gpt-4o-mini" {
					t.Fatalf("ModelName() = %q, want %q", got, "gpt-4o-mini")
				}
			},
		},
		{
			name: "anthropic",
			cfg: llm.ProviderConfig{
				Type:    "anthropic",
				APIKey:  "anthropic-key",
				BaseURL: "https://example.anthropic",
				Model:   "claude-3-5-haiku-latest",
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*anthropicllm.AnthropicClient)
				if !ok {
					t.Fatalf("provider type = %T, want *AnthropicClient", provider)
				}
				if got := typed.ModelName(); got != "claude-3-5-haiku-latest" {
					t.Fatalf("ModelName() = %q, want %q", got, "claude-3-5-haiku-latest")
				}
			},
		},
		{
			name: "gemini",
			cfg: llm.ProviderConfig{
				Type:    "gemini",
				APIKey:  "gemini-key",
				BaseURL: "https://example.gemini",
				Model:   "gemini-2.0-flash",
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*geminillm.GeminiClient)
				if !ok {
					t.Fatalf("provider type = %T, want *GeminiClient", provider)
				}
				if got := typed.ModelName(); got != "gemini-2.0-flash" {
					t.Fatalf("ModelName() = %q, want %q", got, "gemini-2.0-flash")
				}
			},
		},
		{
			name: "echo",
			cfg: llm.ProviderConfig{
				Type:  "echo",
				Model: "echo-m6",
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*llm.EchoChatProvider)
				if !ok {
					t.Fatalf("provider type = %T, want *EchoChatProvider", provider)
				}
				if got := typed.ModelName(); got != "echo-m6" {
					t.Fatalf("ModelName() = %q, want %q", got, "echo-m6")
				}
			},
		},
		{
			name: "scripted_echo",
			cfg: llm.ProviderConfig{
				Type: "scripted_echo",
				ScriptedResponses: []llm.ChatResponse{
					{
						Content:    types.ContentParts{types.TextPart{Text: "scripted-factory"}},
						StopReason: "stop",
					},
				},
			},
			assertProvider: func(t *testing.T, provider llm.ChatProvider) {
				t.Helper()
				typed, ok := provider.(*llm.ScriptedEchoChatProvider)
				if !ok {
					t.Fatalf("provider type = %T, want *ScriptedEchoChatProvider", provider)
				}
				resp, err := typed.Chat(context.Background(), llm.ChatRequest{})
				if err != nil {
					t.Fatalf("Chat() error = %v", err)
				}
				if got := strings.TrimSpace(textFromContentParts(resp.Content)); got != "scripted-factory" {
					t.Fatalf("response text = %q, want %q", got, "scripted-factory")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			provider, err := llm.NewProvider(tc.cfg)
			if err != nil {
				t.Fatalf("NewProvider(%q) error = %v", tc.cfg.Type, err)
			}
			tc.assertProvider(t, provider)
		})
	}
}

func TestScriptedEchoProvider(t *testing.T) {
	t.Run("echo mirrors user message", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		provider, err := llm.NewProvider(llm.ProviderConfig{
			Type:  "echo",
			Model: "echo-scripted-m6",
		})
		if err != nil {
			t.Fatalf("NewProvider(echo) error = %v", err)
		}

		ctxStore := soul.NewSoulContext(t.TempDir())
		engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")

		result, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "ECHO_PROVIDER_OK_2026"}})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "ECHO_PROVIDER_OK_2026" {
			t.Fatalf("result text = %q, want %q", got, "ECHO_PROVIDER_OK_2026")
		}
	})

	t.Run("scripted sequence returns responses in order", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		provider, err := llm.NewProvider(llm.ProviderConfig{
			Type: "scripted_echo",
			ScriptedResponses: []llm.ChatResponse{
				{Content: types.ContentParts{types.TextPart{Text: "SCRIPTED_SEQ_FIRST_2026"}}, StopReason: "stop"},
				{Content: types.ContentParts{types.TextPart{Text: "SCRIPTED_SEQ_SECOND_2026"}}, StopReason: "stop"},
			},
		})
		if err != nil {
			t.Fatalf("NewProvider(scripted_echo) error = %v", err)
		}

		ctxStore := soul.NewSoulContext(t.TempDir())
		engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")

		first, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "first"}})
		if err != nil {
			t.Fatalf("first Run() error = %v", err)
		}
		if got := strings.TrimSpace(textFromContentParts(first.Content)); got != "SCRIPTED_SEQ_FIRST_2026" {
			t.Fatalf("first response text = %q, want %q", got, "SCRIPTED_SEQ_FIRST_2026")
		}

		second, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "second"}})
		if err != nil {
			t.Fatalf("second Run() error = %v", err)
		}
		if got := strings.TrimSpace(textFromContentParts(second.Content)); got != "SCRIPTED_SEQ_SECOND_2026" {
			t.Fatalf("second response text = %q, want %q", got, "SCRIPTED_SEQ_SECOND_2026")
		}
	})
}

func TestScriptedOpenAIToolCall(t *testing.T) {
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want %q", got, "text/event-stream")
		}

		payload := m6DecodeJSONRequest(t, r)
		messages := m6JSONList(payload["messages"])
		callIndex := atomic.AddInt32(&requestCount, 1)

		w.Header().Set("Content-Type", "text/event-stream")
		switch callIndex {
		case 1:
			if len(messages) != 1 {
				t.Fatalf("first request messages len = %d, want 1", len(messages))
			}
			if role := strings.TrimSpace(m6JSONString(messages[0], "role")); role != "user" {
				t.Fatalf("first request message role = %q, want user", role)
			}
			if len(m6JSONList(payload["tools"])) != 1 {
				t.Fatalf("first request tools len = %d, want 1", len(m6JSONList(payload["tools"])))
			}
			m6WriteSSEFrames(t, w, []string{
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\\\"openai\\\"}\"}}]}}]}",
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}",
				"data: [DONE]",
			})
		case 2:
			if !m6OpenAIHasToolResultMessage(messages, "call_1") {
				t.Fatalf("second request missing tool result message: %#v", messages)
			}
			m6WriteSSEFrames(t, w, []string{
				"data: {\"choices\":[{\"delta\":{\"content\":\"OPENAI_TOOL_FLOW_OK_2026\"}}]}",
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":3,\"total_tokens\":9}}",
				"data: [DONE]",
			})
		default:
			t.Fatalf("unexpected request count = %d", callIndex)
		}
	}))
	defer server.Close()

	provider := openaillm.NewOpenAIClient("test-openai-key", server.URL+"/v1", "gpt-4o-mini")

	var executed int32
	registry := scriptedToolRegistry{
		definitions: []llm.ToolDefinition{{
			Name: "search",
			Parameters: map[string]any{
				"type": "object",
			},
		}},
		executors: map[string]soul.ToolExecutor{
			"search": toolExecutorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				atomic.AddInt32(&executed, 1)
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: "openai-tool-output",
					},
				}, nil
			}),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, registry, wire.NoopEmitter{}, "")
	result, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "call search and finish"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); !strings.Contains(strings.ToUpper(got), "OPENAI_TOOL_FLOW_OK_2026") {
		t.Fatalf("result text = %q, want contains OPENAI_TOOL_FLOW_OK_2026", got)
	}
	if got := atomic.LoadInt32(&executed); got != 1 {
		t.Fatalf("tool executed count = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("provider request count = %d, want 2", got)
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	if messages[2].Role != soul.RoleTool || messages[2].ToolCallID != "call_1" {
		t.Fatalf("tool context message = %#v, want role=tool tool_call_id=call_1", messages[2])
	}
}

func TestScriptedAnthropicThinking(t *testing.T) {
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want %q", got, "text/event-stream")
		}

		payload := m6DecodeJSONRequest(t, r)
		if payload["thinking"] == nil {
			t.Fatalf("thinking config = nil, want enabled config")
		}
		if messages := m6JSONList(payload["messages"]); len(messages) != 1 {
			t.Fatalf("messages len = %d, want 1", len(messages))
		}

		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		m6WriteSSEFrames(t, w, []string{
			"event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":2}}}",
			"event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"thinking\"}}",
			"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"anthropic-plan-2026\"}}",
			"event: content_block_stop\ndata: {\"index\":0}",
			"event: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"text\"}}",
			"event: content_block_delta\ndata: {\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"ANTHROPIC_THINKING_OK_2026\"}}",
			"event: content_block_stop\ndata: {\"index\":1}",
			"event: message_delta\ndata: {\"usage\":{\"output_tokens\":3},\"delta\":{\"stop_reason\":\"end_turn\"}}",
			"event: message_stop\ndata: {}",
		})
	}))
	defer server.Close()

	baseProvider := anthropicllm.NewAnthropicClient("test-anthropic-key", server.URL, "claude-3-5-haiku-latest")
	provider := llm.WithThinking(baseProvider, llm.ThinkingHigh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")
	result, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "reply with thinking and token"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); !strings.Contains(strings.ToUpper(got), "ANTHROPIC_THINKING_OK_2026") {
		t.Fatalf("result text = %q, want contains ANTHROPIC_THINKING_OK_2026", got)
	}
	if !m6HasThinkPart(result.Content, "anthropic-plan-2026") {
		t.Fatalf("result content missing think part anthropic-plan-2026: %#v", result.Content)
	}
	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("provider request count = %d, want 1", got)
	}

	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("context message count = %d, want 2", len(messages))
	}
	if !m6HasThinkPart(messages[1].Content, "anthropic-plan-2026") {
		t.Fatalf("assistant context missing think part anthropic-plan-2026: %#v", messages[1].Content)
	}
}

func TestScriptedGeminiFunction(t *testing.T) {
	var requestCount int32

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

		payload := m6DecodeJSONRequest(t, r)
		callIndex := atomic.AddInt32(&requestCount, 1)

		w.Header().Set("Content-Type", "text/event-stream")
		switch callIndex {
		case 1:
			if len(m6JSONList(payload["tools"])) != 1 {
				t.Fatalf("first request tools len = %d, want 1", len(m6JSONList(payload["tools"])))
			}
			m6WriteSSEFrames(t, w, []string{
				"data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"functionCall\":{\"id\":\"call_1\",\"name\":\"search\",\"args\":{\"q\":\"gemini\"}}}]}}]}",
				"data: {\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}",
				"data: [DONE]",
			})
		case 2:
			if !m6GeminiHasFunctionResponse(m6JSONList(payload["contents"]), "search") {
				t.Fatalf("second request missing function response for search: %#v", payload)
			}
			m6WriteSSEFrames(t, w, []string{
				"data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"GEMINI_FUNCTION_OK_2026\"}]}}]}",
				"data: {\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":2,\"totalTokenCount\":4}}",
				"data: [DONE]",
			})
		default:
			t.Fatalf("unexpected request count = %d", callIndex)
		}
	}))
	defer server.Close()

	provider := geminillm.NewGeminiClient("test-gemini-key", server.URL, "gemini-2.0-flash")

	var executed int32
	registry := scriptedToolRegistry{
		definitions: []llm.ToolDefinition{{
			Name: "search",
			Parameters: map[string]any{
				"type": "object",
			},
		}},
		executors: map[string]soul.ToolExecutor{
			"search": toolExecutorFunc(func(_ context.Context, call types.ToolCall) (types.ToolResult, error) {
				atomic.AddInt32(&executed, 1)
				return types.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Value: types.ToolReturnValue{
						Value: map[string]any{"content": "gemini-tool-output"},
					},
				}, nil
			}),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, registry, wire.NoopEmitter{}, "")
	result, err := engine.Run(ctx, types.ContentParts{types.TextPart{Text: "call search and return token"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); !strings.Contains(strings.ToUpper(got), "GEMINI_FUNCTION_OK_2026") {
		t.Fatalf("result text = %q, want contains GEMINI_FUNCTION_OK_2026", got)
	}
	if got := atomic.LoadInt32(&executed); got != 1 {
		t.Fatalf("tool executed count = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("provider request count = %d, want 2", got)
	}
}

func m6DecodeJSONRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode request json: %v\npayload=%s", err, payload)
	}
	return decoded
}

func m6WriteSSEFrames(t *testing.T, w http.ResponseWriter, frames []string) {
	t.Helper()
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer must support flush")
	}
	for i := range frames {
		if _, err := fmt.Fprint(w, frames[i], "\n\n"); err != nil {
			t.Fatalf("write sse frame: %v", err)
		}
		flusher.Flush()
	}
}

func m6JSONList(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		obj, ok := items[i].(map[string]any)
		if !ok {
			continue
		}
		out = append(out, obj)
	}
	return out
}

func m6JSONString(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	text, _ := obj[key].(string)
	return text
}

func m6OpenAIHasToolResultMessage(messages []map[string]any, callID string) bool {
	for i := range messages {
		role, _ := messages[i]["role"].(string)
		toolCallID, _ := messages[i]["tool_call_id"].(string)
		if strings.TrimSpace(role) == "tool" && strings.TrimSpace(toolCallID) == strings.TrimSpace(callID) {
			return true
		}
	}
	return false
}

func m6GeminiHasFunctionResponse(contents []map[string]any, functionName string) bool {
	for i := range contents {
		parts := m6JSONList(contents[i]["parts"])
		for j := range parts {
			responseObj, ok := parts[j]["functionResponse"].(map[string]any)
			if !ok {
				continue
			}
			name, _ := responseObj["name"].(string)
			if strings.TrimSpace(name) == strings.TrimSpace(functionName) {
				return true
			}
		}
	}
	return false
}

func m6HasThinkPart(parts types.ContentParts, needle string) bool {
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.ThinkPart:
			if strings.Contains(strings.ToUpper(typed.Think), strings.ToUpper(needle)) {
				return true
			}
		case *types.ThinkPart:
			if typed != nil && strings.Contains(strings.ToUpper(typed.Think), strings.ToUpper(needle)) {
				return true
			}
		}
	}
	return false
}
