package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	defaultBaseURL        = "https://api.openai.com/v1"
	defaultModel          = "gpt-4o-mini"
	defaultRequestPath    = "/chat/completions"
	defaultRequestTO      = 120 * time.Second
	defaultInitialBackoff = 300 * time.Millisecond
	defaultMaxAttempts    = 3
	streamScannerMaxSize  = 4 * 1024 * 1024
)

// OpenAIClient calls the OpenAI chat completions API.
type OpenAIClient struct {
	apiKey         string
	baseURL        string
	model          string
	httpClient     *http.Client
	thinkingEffort string
	maxAttempts    int
	initialBackoff time.Duration
}

var _ llm.ChatProvider = (*OpenAIClient)(nil)

// NewOpenAIClient creates an OpenAI HTTP client with sensible defaults.
func NewOpenAIClient(apiKey, baseURL, model string) *OpenAIClient {
	normalizedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalizedBaseURL == "" {
		normalizedBaseURL = defaultBaseURL
	}
	normalizedModel := strings.TrimSpace(model)
	if normalizedModel == "" {
		normalizedModel = defaultModel
	}

	return &OpenAIClient{
		apiKey:         strings.TrimSpace(apiKey),
		baseURL:        normalizedBaseURL,
		model:          normalizedModel,
		httpClient:     &http.Client{Timeout: defaultRequestTO},
		maxAttempts:    defaultMaxAttempts,
		initialBackoff: defaultInitialBackoff,
	}
}

// NewOpenAIClientFromConfig builds an OpenAI client from provider/model config.
func NewOpenAIClientFromConfig(provider config.LLMProvider, model config.LLMModel) *OpenAIClient {
	return NewOpenAIClient(provider.APIKey, provider.BaseURL, model.Name)
}

// ModelName returns the configured model identifier.
func (c *OpenAIClient) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// WithModel returns a cloned provider with updated model identifier.
func (c *OpenAIClient) WithModel(model string) llm.ChatProvider {
	if c == nil {
		return c
	}
	cloned := *c
	if normalized := strings.TrimSpace(model); normalized != "" {
		cloned.model = normalized
	}
	return &cloned
}

// WithThinking returns a cloned provider with updated thinking effort.
func (c *OpenAIClient) WithThinking(effort string) llm.ChatProvider {
	if c == nil {
		return c
	}
	cloned := *c
	cloned.thinkingEffort = strings.TrimSpace(effort)
	return &cloned
}

// Chat performs a non-streaming chat completion call.
func (c *OpenAIClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if c == nil {
		return nil, errors.New("openai client: nil")
	}

	payload := c.buildChatCompletionRequest(req, false)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai chat: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var decoded chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("openai chat: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, errors.New("openai chat: missing choices in response")
	}

	firstChoice := decoded.Choices[0]
	response := &llm.ChatResponse{
		Content:    decodeMessageContent(firstChoice.Message.Content, firstChoice.Message.ReasoningContent),
		ToolCalls:  decodeToolCalls(firstChoice.Message.ToolCalls),
		Usage:      decodeTokenUsage(decoded.Usage),
		StopReason: firstChoice.FinishReason,
	}

	return response, nil
}

// ChatStream performs a streaming chat completion call and returns SSE events.
func (c *OpenAIClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	if c == nil {
		return nil, errors.New("openai client: nil")
	}

	payload := c.buildChatCompletionRequest(req, true)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai chat stream: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, true)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.ChatEvent)
	go c.consumeStream(ctx, resp.Body, events)
	return events, nil
}

func (c *OpenAIClient) consumeStream(ctx context.Context, body io.ReadCloser, out chan<- llm.ChatEvent) {
	defer close(out)
	defer func() {
		_ = body.Close()
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxSize)

	toolCalls := map[int]*streamToolCall{}
	var finalUsage *types.TokenUsage

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			c.emitPendingToolCalls(ctx, out, toolCalls)
			c.emitEvent(ctx, out, llm.ChatEvent{Usage: finalUsage, Done: true})
			return
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("openai chat stream: decode chunk: %w", err), Done: true})
			return
		}
		if chunk.Usage != nil {
			usage := decodeTokenUsage(chunk.Usage)
			finalUsage = &usage
		}

		for _, choice := range chunk.Choices {
			if text := strings.TrimSpace(choice.Delta.Content); text != "" {
				if !c.emitEvent(ctx, out, llm.ChatEvent{Delta: types.TextPart{Text: text}}) {
					return
				}
			}
			if thought := strings.TrimSpace(choice.Delta.ReasoningContent); thought != "" {
				if !c.emitEvent(ctx, out, llm.ChatEvent{Delta: types.ThinkPart{Think: thought}}) {
					return
				}
			}

			for _, toolDelta := range choice.Delta.ToolCalls {
				streamCall := toolCalls[toolDelta.Index]
				if streamCall == nil {
					streamCall = &streamToolCall{}
					toolCalls[toolDelta.Index] = streamCall
				}
				if strings.TrimSpace(toolDelta.ID) != "" {
					streamCall.ID = strings.TrimSpace(toolDelta.ID)
				}
				if strings.TrimSpace(toolDelta.Function.Name) != "" {
					streamCall.Name = strings.TrimSpace(toolDelta.Function.Name)
				}
				if toolDelta.Function.Arguments != "" {
					streamCall.Arguments.WriteString(toolDelta.Function.Arguments)
				}
			}

			if choice.FinishReason == "tool_calls" {
				c.emitPendingToolCalls(ctx, out, toolCalls)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("openai chat stream: read stream: %w", err), Done: true})
		return
	}

	c.emitPendingToolCalls(ctx, out, toolCalls)
	c.emitEvent(ctx, out, llm.ChatEvent{Usage: finalUsage, Done: true})
}

func (c *OpenAIClient) emitPendingToolCalls(ctx context.Context, out chan<- llm.ChatEvent, pending map[int]*streamToolCall) {
	if len(pending) == 0 {
		return
	}

	indices := make([]int, 0, len(pending))
	for idx := range pending {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	for _, idx := range indices {
		call := pending[idx]
		if call == nil || call.emitted {
			continue
		}
		call.emitted = true

		toolCallID := call.ID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("tool_call_%d", idx)
		}

		event := llm.ChatEvent{
			ToolCall: &types.ToolCall{
				ID:        toolCallID,
				Name:      call.Name,
				Arguments: parseToolArguments(call.Arguments.String()),
			},
		}
		if !c.emitEvent(ctx, out, event) {
			return
		}
	}
}

func (c *OpenAIClient) emitEvent(ctx context.Context, out chan<- llm.ChatEvent, event llm.ChatEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func (c *OpenAIClient) doRequestWithRetry(ctx context.Context, payload []byte, stream bool) (*http.Response, error) {
	attempts := c.maxAttempts
	if attempts < 1 {
		attempts = defaultMaxAttempts
	}
	backoff := c.initialBackoff
	if backoff <= 0 {
		backoff = defaultInitialBackoff
	}

	endpoint := c.endpointURL()
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("openai request: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		client := c.httpClientForMode(stream)
		resp, err := client.Do(req)
		if err != nil {
			if attempt < attempts && isRetryableTransportError(err) {
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return nil, sleepErr
				}
				backoff *= 2
				continue
			}
			return nil, fmt.Errorf("openai request: %w", err)
		}

		if isRetryableStatusCode(resp.StatusCode) && attempt < attempts {
			discardAndClose(resp.Body)
			if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
				return nil, sleepErr
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := strings.TrimSpace(readBodyForError(resp.Body))
			discardAndClose(resp.Body)
			if message == "" {
				message = http.StatusText(resp.StatusCode)
			}
			return nil, fmt.Errorf("openai request: status %d: %s", resp.StatusCode, message)
		}

		return resp, nil
	}

	return nil, errors.New("openai request: exhausted retries")
}

func (c *OpenAIClient) httpClientForMode(stream bool) *http.Client {
	if c.httpClient == nil {
		if stream {
			return &http.Client{}
		}
		return &http.Client{Timeout: defaultRequestTO}
	}
	if !stream {
		return c.httpClient
	}

	clone := *c.httpClient
	clone.Timeout = 0
	return &clone
}

func (c *OpenAIClient) endpointURL() string {
	baseURL := strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if strings.HasSuffix(baseURL, defaultRequestPath) {
		return baseURL
	}
	return baseURL + defaultRequestPath
}

func (c *OpenAIClient) buildChatCompletionRequest(req llm.ChatRequest, stream bool) chatCompletionRequest {
	messages := make([]chatCompletionMessageInput, 0, len(req.Messages))
	for _, msg := range req.Messages {
		messages = append(messages, chatCompletionMessageInput{
			Role:       msg.Role,
			Content:    encodeMessageContent(msg.Content),
			ToolCalls:  encodeMessageToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallID,
		})
	}

	tools := make([]chatCompletionTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, chatCompletionTool{
			Type: "function",
			Function: chatCompletionToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	request := chatCompletionRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   stream,
	}
	if req.Temperature != 0 {
		temp := req.Temperature
		request.Temperature = &temp
	}
	if req.MaxTokens > 0 {
		request.MaxTokens = req.MaxTokens
	}
	if stream {
		request.StreamOptions = &chatCompletionStreamOptions{IncludeUsage: true}
	}
	if effort := strings.TrimSpace(c.thinkingEffort); effort != "" {
		request.ReasoningEffort = effort
	}

	return request
}

func encodeMessageContent(parts types.ContentParts) any {
	if len(parts) == 0 {
		return ""
	}

	if len(parts) == 1 {
		switch part := parts[0].(type) {
		case types.TextPart:
			return part.Text
		case *types.TextPart:
			if part != nil {
				return part.Text
			}
		}
	}

	encoded := make([]chatCompletionContentPart, 0, len(parts))
	for _, part := range parts {
		switch typed := part.(type) {
		case types.TextPart:
			encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeText), Text: typed.Text})
		case *types.TextPart:
			if typed != nil {
				encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeText), Text: typed.Text})
			}
		case types.ImageURLPart:
			encoded = append(encoded, chatCompletionContentPart{
				Type: string(types.ContentPartTypeImageURL),
				ImageURL: &chatCompletionURLValue{
					URL: typed.ImageURL,
				},
			})
		case *types.ImageURLPart:
			if typed != nil {
				encoded = append(encoded, chatCompletionContentPart{
					Type: string(types.ContentPartTypeImageURL),
					ImageURL: &chatCompletionURLValue{
						URL: typed.ImageURL,
					},
				})
			}
		case types.AudioURLPart:
			encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeAudioURL), AudioURL: typed.AudioURL})
		case *types.AudioURLPart:
			if typed != nil {
				encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeAudioURL), AudioURL: typed.AudioURL})
			}
		case types.VideoURLPart:
			encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeVideoURL), VideoURL: typed.VideoURL})
		case *types.VideoURLPart:
			if typed != nil {
				encoded = append(encoded, chatCompletionContentPart{Type: string(types.ContentPartTypeVideoURL), VideoURL: typed.VideoURL})
			}
		}
	}

	if len(encoded) == 0 {
		return ""
	}
	return encoded
}

func encodeMessageToolCalls(calls []types.ToolCall) []chatCompletionToolCall {
	if len(calls) == 0 {
		return nil
	}

	encoded := make([]chatCompletionToolCall, 0, len(calls))
	for i := range calls {
		callID := strings.TrimSpace(calls[i].ID)
		callName := strings.TrimSpace(calls[i].Name)
		if callID == "" || callName == "" {
			continue
		}
		encoded = append(encoded, chatCompletionToolCall{
			ID:   callID,
			Type: "function",
			Function: chatCompletionToolFunctionCall{
				Name:      callName,
				Arguments: encodeToolArguments(calls[i].Arguments),
			},
		})
	}
	if len(encoded) == 0 {
		return nil
	}
	return encoded
}

func encodeToolArguments(args types.JsonType) string {
	if args == nil {
		return "{}"
	}

	if raw, ok := args.(string); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "{}"
		}
		if json.Valid([]byte(trimmed)) {
			return trimmed
		}
		encoded, err := json.Marshal(trimmed)
		if err != nil {
			return "{}"
		}
		return string(encoded)
	}

	encoded, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	return trimmed
}

func decodeMessageContent(raw any, reasoning string) types.ContentParts {
	parts := make(types.ContentParts, 0, 4)
	if trimmedReasoning := strings.TrimSpace(reasoning); trimmedReasoning != "" {
		parts = append(parts, types.ThinkPart{Think: trimmedReasoning})
	}

	switch content := raw.(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			parts = append(parts, types.TextPart{Text: content})
		}
	case []any:
		for _, item := range content {
			decoded := decodeContentPartItem(item)
			if decoded != nil {
				parts = append(parts, decoded)
			}
		}
	case map[string]any:
		if decoded := decodeContentPartItem(content); decoded != nil {
			parts = append(parts, decoded)
		}
	}

	return parts
}

func decodeContentPartItem(item any) types.ContentPart {
	obj, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	partType, _ := obj["type"].(string)
	partType = strings.TrimSpace(partType)

	switch partType {
	case string(types.ContentPartTypeText), "":
		if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
			return types.TextPart{Text: text}
		}
	case string(types.ContentPartTypeThink):
		if think, ok := obj["think"].(string); ok && strings.TrimSpace(think) != "" {
			return types.ThinkPart{Think: think}
		}
	case string(types.ContentPartTypeImageURL):
		if imageURL, ok := obj["image_url"].(string); ok && strings.TrimSpace(imageURL) != "" {
			return types.ImageURLPart{ImageURL: imageURL}
		}
		if imageObj, ok := obj["image_url"].(map[string]any); ok {
			if imageURL, ok := imageObj["url"].(string); ok && strings.TrimSpace(imageURL) != "" {
				return types.ImageURLPart{ImageURL: imageURL}
			}
		}
	case string(types.ContentPartTypeAudioURL):
		if audioURL, ok := obj["audio_url"].(string); ok && strings.TrimSpace(audioURL) != "" {
			return types.AudioURLPart{AudioURL: audioURL}
		}
	case string(types.ContentPartTypeVideoURL):
		if videoURL, ok := obj["video_url"].(string); ok && strings.TrimSpace(videoURL) != "" {
			return types.VideoURLPart{VideoURL: videoURL}
		}
	}

	return nil
}

func decodeToolCalls(raw []chatCompletionToolCall) []types.ToolCall {
	if len(raw) == 0 {
		return nil
	}

	toolCalls := make([]types.ToolCall, 0, len(raw))
	for _, call := range raw {
		toolCalls = append(toolCalls, types.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: parseToolArguments(call.Function.Arguments),
		})
	}
	return toolCalls
}

func parseToolArguments(raw string) types.JsonType {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var value types.JsonType
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}
	return value
}

func decodeTokenUsage(usage *chatCompletionUsage) types.TokenUsage {
	if usage == nil {
		return types.TokenUsage{}
	}
	return types.TokenUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
}

func isRetryableStatusCode(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func isRetryableTransportError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return true
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readBodyForError(body io.Reader) string {
	if body == nil {
		return ""
	}
	limited := io.LimitReader(body, 128*1024)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return ""
	}
	return string(payload)
}

func discardAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

type chatCompletionRequest struct {
	Model           string                       `json:"model"`
	Messages        []chatCompletionMessageInput `json:"messages"`
	Tools           []chatCompletionTool         `json:"tools,omitempty"`
	Temperature     *float64                     `json:"temperature,omitempty"`
	MaxTokens       int                          `json:"max_tokens,omitempty"`
	Stream          bool                         `json:"stream,omitempty"`
	StreamOptions   *chatCompletionStreamOptions `json:"stream_options,omitempty"`
	ReasoningEffort string                       `json:"reasoning_effort,omitempty"`
}

type chatCompletionMessageInput struct {
	Role       string                   `json:"role"`
	Content    any                      `json:"content,omitempty"`
	ToolCalls  []chatCompletionToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                   `json:"tool_call_id,omitempty"`
}

type chatCompletionContentPart struct {
	Type     string                  `json:"type"`
	Text     string                  `json:"text,omitempty"`
	Think    string                  `json:"think,omitempty"`
	ImageURL *chatCompletionURLValue `json:"image_url,omitempty"`
	AudioURL string                  `json:"audio_url,omitempty"`
	VideoURL string                  `json:"video_url,omitempty"`
}

type chatCompletionURLValue struct {
	URL string `json:"url"`
}

type chatCompletionTool struct {
	Type     string                     `json:"type"`
	Function chatCompletionToolFunction `json:"function"`
}

type chatCompletionToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  types.JsonType `json:"parameters,omitempty"`
}

type chatCompletionStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatCompletionResponse struct {
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage,omitempty"`
}

type chatCompletionChoice struct {
	Message      chatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type chatCompletionMessage struct {
	Role             string                   `json:"role,omitempty"`
	Content          any                      `json:"content,omitempty"`
	ReasoningContent string                   `json:"reasoning_content,omitempty"`
	ToolCalls        []chatCompletionToolCall `json:"tool_calls,omitempty"`
}

type chatCompletionToolCall struct {
	ID       string                         `json:"id"`
	Type     string                         `json:"type,omitempty"`
	Function chatCompletionToolFunctionCall `json:"function"`
}

type chatCompletionToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type chatCompletionChunk struct {
	Choices []chatCompletionChunkChoice `json:"choices"`
	Usage   *chatCompletionUsage        `json:"usage,omitempty"`
}

type chatCompletionChunkChoice struct {
	Delta        chatCompletionChunkDelta `json:"delta"`
	FinishReason string                   `json:"finish_reason,omitempty"`
}

type chatCompletionChunkDelta struct {
	Content          string                        `json:"content,omitempty"`
	ReasoningContent string                        `json:"reasoning_content,omitempty"`
	ToolCalls        []chatCompletionChunkToolCall `json:"tool_calls,omitempty"`
}

type chatCompletionChunkToolCall struct {
	Index    int                             `json:"index"`
	ID       string                          `json:"id,omitempty"`
	Type     string                          `json:"type,omitempty"`
	Function chatCompletionChunkToolFunction `json:"function"`
}

type chatCompletionChunkToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
	emitted   bool
}
