package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	llmhttputil "github.com/wanpengxie/go-kimi/pkg/kimi/llm/internal/httputil"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	defaultBaseURL          = "https://api.anthropic.com"
	defaultModel            = "claude-3-5-haiku-latest"
	defaultRequestPath      = "/v1/messages"
	defaultAnthropicVersion = "2023-06-01"
	defaultRequestTO        = 120 * time.Second
	defaultInitialBackoff   = 300 * time.Millisecond
	defaultMaxAttempts      = 3
	defaultMaxTokens        = 4096
	streamScannerMaxSize    = 4 * 1024 * 1024
)

const (
	thinkingBudgetLow    = 512
	thinkingBudgetMedium = 1024
	thinkingBudgetHigh   = 2048
)

// AnthropicClient calls the Anthropic Claude messages API.
type AnthropicClient struct {
	apiKey           string
	baseURL          string
	model            string
	httpClient       *http.Client
	thinkingEffort   llm.ThinkingEffort
	anthropicVersion string
	maxAttempts      int
	initialBackoff   time.Duration
}

var _ llm.ChatProvider = (*AnthropicClient)(nil)
var _ llm.ThinkingProvider = (*AnthropicClient)(nil)

// NewAnthropicClient creates an Anthropic HTTP client with sensible defaults.
func NewAnthropicClient(apiKey, baseURL, model string) *AnthropicClient {
	normalizedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalizedBaseURL == "" {
		normalizedBaseURL = defaultBaseURL
	}
	normalizedModel := strings.TrimSpace(model)
	if normalizedModel == "" {
		normalizedModel = defaultModel
	}

	return &AnthropicClient{
		apiKey:           strings.TrimSpace(apiKey),
		baseURL:          normalizedBaseURL,
		model:            normalizedModel,
		httpClient:       &http.Client{Timeout: defaultRequestTO},
		thinkingEffort:   llm.ThinkingOff,
		anthropicVersion: defaultAnthropicVersion,
		maxAttempts:      defaultMaxAttempts,
		initialBackoff:   defaultInitialBackoff,
	}
}

// NewAnthropicClientFromConfig builds an Anthropic client from provider/model config.
func NewAnthropicClientFromConfig(provider config.LLMProvider, model config.LLMModel) *AnthropicClient {
	return NewAnthropicClient(provider.APIKey.Raw(), provider.BaseURL, model.Name)
}

// ModelName returns the configured model identifier.
func (c *AnthropicClient) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// WithModel returns a cloned provider with updated model identifier.
func (c *AnthropicClient) WithModel(model string) llm.ChatProvider {
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
func (c *AnthropicClient) WithThinking(effort llm.ThinkingEffort) llm.ChatProvider {
	if c == nil {
		return c
	}
	cloned := *c
	cloned.thinkingEffort = llm.NormalizeThinkingEffort(effort)
	return &cloned
}

// Chat performs a non-streaming messages API call.
func (c *AnthropicClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if c == nil {
		return nil, errors.New("anthropic client: nil")
	}

	payload := c.buildMessagesRequest(req, false)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var decoded messagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("anthropic chat: decode response: %w", err)
	}

	response := &llm.ChatResponse{
		Content:    decodeMessageContent(decoded.Content),
		ToolCalls:  decodeToolCalls(decoded.Content),
		Usage:      decodeTokenUsage(decoded.Usage),
		StopReason: normalizeStopReason(decoded.StopReason),
	}
	return response, nil
}

// ChatStream performs a streaming messages API call and returns SSE events.
func (c *AnthropicClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	if c == nil {
		return nil, errors.New("anthropic client: nil")
	}

	payload := c.buildMessagesRequest(req, true)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat stream: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, true)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.ChatEvent)
	go c.consumeStream(ctx, resp.Body, events)
	return events, nil
}

func (c *AnthropicClient) consumeStream(ctx context.Context, body io.ReadCloser, out chan<- llm.ChatEvent) {
	defer close(out)
	defer func() {
		_ = body.Close()
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxSize)

	toolBlocks := map[int]*streamToolUse{}
	thinkingBlocks := map[int]*streamThinking{}
	var usage *types.TokenUsage
	var eventName string
	dataLines := make([]string, 0, 2)
	doneEmitted := false

	handleFrame := func() bool {
		if len(dataLines) == 0 {
			eventName = ""
			return false
		}

		frameData := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		stop := c.handleStreamFrame(ctx, out, strings.TrimSpace(eventName), frameData, toolBlocks, thinkingBlocks, &usage, &doneEmitted)
		eventName = ""
		return stop
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			if handleFrame() {
				return
			}
			continue
		}
		if strings.HasPrefix(trimmedLine, ":") {
			continue
		}
		if strings.HasPrefix(trimmedLine, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(trimmedLine, "event:"))
			continue
		}
		if strings.HasPrefix(trimmedLine, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			dataLines = append(dataLines, payload)
		}
	}

	if err := scanner.Err(); err != nil {
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: read stream: %w", err), Done: true})
		return
	}

	if handleFrame() {
		return
	}

	if !doneEmitted {
		c.emitPendingToolCalls(ctx, out, toolBlocks)
		c.emitEvent(ctx, out, llm.ChatEvent{Usage: usage, Done: true})
	}
}

func (c *AnthropicClient) handleStreamFrame(
	ctx context.Context,
	out chan<- llm.ChatEvent,
	eventName string,
	data string,
	toolBlocks map[int]*streamToolUse,
	thinkingBlocks map[int]*streamThinking,
	usage **types.TokenUsage,
	doneEmitted *bool,
) bool {
	trimmedData := strings.TrimSpace(data)
	if trimmedData == "" {
		return false
	}
	if trimmedData == "[DONE]" {
		c.emitPendingToolCalls(ctx, out, toolBlocks)
		c.emitEvent(ctx, out, llm.ChatEvent{Usage: *usage, Done: true})
		*doneEmitted = true
		return true
	}

	switch eventName {
	case "", "message":
		return false
	case "ping":
		return false
	case "message_start":
		var payload streamMessageStartEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode message_start: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		*usage = mergeUsage(*usage, payload.Message.Usage)
		return false
	case "message_delta":
		var payload streamMessageDeltaEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode message_delta: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		*usage = mergeUsage(*usage, payload.Usage)
		return false
	case "content_block_start":
		var payload streamContentBlockStartEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode content_block_start: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		blockType := strings.TrimSpace(payload.ContentBlock.Type)
		if blockType == "" {
			return false
		}
		// Thinking blocks need an internal builder so we can collect
		// thinking_delta + signature_delta chunks and emit one complete
		// ThinkPart at content_block_stop. Tool-use blocks keep the
		// existing per-index streamToolUse builder.
		if blockType == "thinking" {
			thinkingBlocks[payload.Index] = &streamThinking{}
			return false
		}
		block := &streamToolUse{
			Type: blockType,
			ID:   strings.TrimSpace(payload.ContentBlock.ID),
			Name: strings.TrimSpace(payload.ContentBlock.Name),
		}
		if payload.ContentBlock.Input != nil {
			block.Arguments.WriteString(encodeToolInput(payload.ContentBlock.Input))
			block.hasInitialInput = true
		}
		toolBlocks[payload.Index] = block
		return false
	case "content_block_delta":
		var payload streamContentBlockDeltaEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode content_block_delta: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		deltaType := strings.TrimSpace(payload.Delta.Type)
		switch deltaType {
		case "text_delta":
			if payload.Delta.Text != "" {
				if !c.emitEvent(ctx, out, llm.ChatEvent{Delta: types.TextPart{Text: payload.Delta.Text}}) {
					return true
				}
			}
		case "thinking_delta":
			// Accumulate text into the per-block builder so the final
			// ThinkPart emitted at content_block_stop carries both the
			// full thinking text AND the signature attached to it.
			if payload.Delta.Thinking != "" {
				tb := thinkingBlocks[payload.Index]
				if tb == nil {
					tb = &streamThinking{}
					thinkingBlocks[payload.Index] = tb
				}
				tb.think.WriteString(payload.Delta.Thinking)
			}
		case "signature_delta":
			if payload.Delta.Signature != "" {
				tb := thinkingBlocks[payload.Index]
				if tb == nil {
					tb = &streamThinking{}
					thinkingBlocks[payload.Index] = tb
				}
				tb.signature.WriteString(payload.Delta.Signature)
			}
		case "input_json_delta":
			block := toolBlocks[payload.Index]
			if block == nil {
				block = &streamToolUse{Type: "tool_use"}
				toolBlocks[payload.Index] = block
			}
			if !block.hasPartialInput && block.hasInitialInput {
				block.Arguments.Reset()
			}
			block.hasPartialInput = true
			block.Arguments.WriteString(payload.Delta.PartialJSON)
		}
		return false
	case "content_block_stop":
		var payload streamContentBlockStopEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode content_block_stop: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		// Flush a thinking builder if one was opened for this index.
		// Emit ONE ThinkPart per logical thinking block — both Think and
		// Signature populated — so consumers (and downstream history
		// round-trip via buildAssistantContent) see a single, complete
		// block instead of many fragments.
		if tb, ok := thinkingBlocks[payload.Index]; ok {
			delete(thinkingBlocks, payload.Index)
			if tb.think.Len() > 0 || tb.signature.Len() > 0 {
				if !c.emitEvent(ctx, out, llm.ChatEvent{Delta: types.ThinkPart{
					Think:     tb.think.String(),
					Signature: tb.signature.String(),
				}}) {
					return true
				}
			}
			return false
		}
		if !c.emitToolCallByIndex(ctx, out, toolBlocks, payload.Index) {
			return true
		}
		return false
	case "message_stop":
		c.emitPendingToolCalls(ctx, out, toolBlocks)
		c.emitEvent(ctx, out, llm.ChatEvent{Usage: *usage, Done: true})
		*doneEmitted = true
		return true
	case "error":
		var payload streamErrorEvent
		if err := json.Unmarshal([]byte(trimmedData), &payload); err != nil {
			c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: decode error event: %w", err), Done: true})
			*doneEmitted = true
			return true
		}
		message := strings.TrimSpace(payload.Error.Message)
		if message == "" {
			message = "unknown error"
		}
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("anthropic chat stream: %s", message), Done: true})
		*doneEmitted = true
		return true
	default:
		return false
	}
}

func (c *AnthropicClient) emitToolCallByIndex(ctx context.Context, out chan<- llm.ChatEvent, pending map[int]*streamToolUse, idx int) bool {
	block := pending[idx]
	if block == nil || block.emitted || block.Type != "tool_use" {
		return true
	}
	block.emitted = true

	toolCallID := block.ID
	if toolCallID == "" {
		toolCallID = fmt.Sprintf("tool_call_%d", idx)
	}
	event := llm.ChatEvent{
		ToolCall: &types.ToolCall{
			ID:        toolCallID,
			Name:      block.Name,
			Arguments: parseToolInput(block.Arguments.String()),
		},
	}
	return c.emitEvent(ctx, out, event)
}

func (c *AnthropicClient) emitPendingToolCalls(ctx context.Context, out chan<- llm.ChatEvent, pending map[int]*streamToolUse) {
	if len(pending) == 0 {
		return
	}

	indices := make([]int, 0, len(pending))
	for idx := range pending {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	for _, idx := range indices {
		if !c.emitToolCallByIndex(ctx, out, pending, idx) {
			return
		}
	}
}

func (c *AnthropicClient) emitEvent(ctx context.Context, out chan<- llm.ChatEvent, event llm.ChatEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func (c *AnthropicClient) doRequestWithRetry(ctx context.Context, payload []byte, stream bool) (*http.Response, error) {
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
			return nil, fmt.Errorf("anthropic request: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("x-api-key", c.apiKey)
		}
		version := strings.TrimSpace(c.anthropicVersion)
		if version == "" {
			version = defaultAnthropicVersion
		}
		req.Header.Set("anthropic-version", version)
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		client := c.httpClientForMode(stream)
		resp, err := client.Do(req)
		if err != nil {
			if attempt < attempts && llmhttputil.IsRetryableTransportError(err) {
				if sleepErr := llmhttputil.SleepWithContext(ctx, backoff); sleepErr != nil {
					return nil, sleepErr
				}
				backoff *= 2
				continue
			}
			return nil, fmt.Errorf("anthropic request: %w", err)
		}

		if llmhttputil.IsRetryableStatusCode(resp.StatusCode) && attempt < attempts {
			llmhttputil.DiscardAndClose(resp.Body)
			if sleepErr := llmhttputil.SleepWithContext(ctx, backoff); sleepErr != nil {
				return nil, sleepErr
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := strings.TrimSpace(llmhttputil.ReadBodyForError(resp.Body))
			llmhttputil.DiscardAndClose(resp.Body)
			if message == "" {
				message = http.StatusText(resp.StatusCode)
			}
			return nil, fmt.Errorf("anthropic request: status %d: %s", resp.StatusCode, message)
		}

		return resp, nil
	}

	return nil, errors.New("anthropic request: exhausted retries")
}

func (c *AnthropicClient) httpClientForMode(stream bool) *http.Client {
	return llmhttputil.ClientForMode(c.httpClient, stream, defaultRequestTO)
}

func (c *AnthropicClient) endpointURL() string {
	baseURL := strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if strings.HasSuffix(baseURL, defaultRequestPath) {
		return baseURL
	}
	return baseURL + defaultRequestPath
}

func (c *AnthropicClient) buildMessagesRequest(req llm.ChatRequest, stream bool) messagesRequest {
	messages, system := buildMessagesAndSystem(req.Messages)

	tools := make([]messageTool, 0, len(req.Tools))
	for i := range req.Tools {
		tools = append(tools, messageTool{
			Name:        strings.TrimSpace(req.Tools[i].Name),
			Description: req.Tools[i].Description,
			InputSchema: normalizeToolSchema(req.Tools[i].Parameters),
		})
	}

	request := messagesRequest{
		Model:     c.model,
		System:    system,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}
	if request.MaxTokens <= 0 {
		request.MaxTokens = defaultMaxTokens
	}
	if req.Temperature != 0 {
		temperature := req.Temperature
		request.Temperature = &temperature
	}
	if thinking := buildThinkingConfig(c.thinkingEffort, request.MaxTokens); thinking != nil {
		request.Thinking = thinking
	}
	return request
}

func buildMessagesAndSystem(messages []llm.Message) ([]messageInput, string) {
	encoded := make([]messageInput, 0, len(messages))
	systemChunks := make([]string, 0, 2)

	appendMessage := func(role string, content []messageContentBlock) {
		if len(content) == 0 {
			return
		}
		lastIndex := len(encoded) - 1
		if lastIndex >= 0 && encoded[lastIndex].Role == role {
			encoded[lastIndex].Content = append(encoded[lastIndex].Content, content...)
			return
		}
		encoded = append(encoded, messageInput{Role: role, Content: content})
	}

	for i := range messages {
		msg := messages[i]
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if chunk := contentPartsToText(msg.Content); chunk != "" {
				systemChunks = append(systemChunks, chunk)
			}
		case "assistant":
			content := encodeRegularContent(msg.Content)
			toolUse := encodeToolUseBlocks(msg.ToolCalls)
			content = append(content, toolUse...)
			appendMessage("assistant", content)
		case "tool":
			toolResult := encodeToolResultBlock(msg)
			if len(toolResult) > 0 {
				appendMessage("user", toolResult)
			}
		default:
			appendMessage("user", encodeRegularContent(msg.Content))
		}
	}

	return encoded, strings.Join(systemChunks, "\n\n")
}

func encodeRegularContent(parts types.ContentParts) []messageContentBlock {
	if len(parts) == 0 {
		return nil
	}

	content := make([]messageContentBlock, 0, len(parts))
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if typed.Text != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.Text})
			}
		case *types.TextPart:
			if typed != nil && typed.Text != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.Text})
			}
		case types.ThinkPart:
			// Round-trip thinking blocks back to the API. DeepSeek's
			// anthropic-compat endpoint hard-rejects requests whose
			// history loses thinking blocks (the model emitted them and
			// the signature must come back verbatim). The same applies
			// to upstream Anthropic when extended thinking is in use.
			if typed.Think != "" || typed.Signature != "" {
				content = append(content, messageContentBlock{
					Type:      "thinking",
					Thinking:  typed.Think,
					Signature: typed.Signature,
				})
			}
		case *types.ThinkPart:
			if typed != nil && (typed.Think != "" || typed.Signature != "") {
				content = append(content, messageContentBlock{
					Type:      "thinking",
					Thinking:  typed.Think,
					Signature: typed.Signature,
				})
			}
		case types.ImageURLPart:
			if typed.ImageURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.ImageURL})
			}
		case *types.ImageURLPart:
			if typed != nil && typed.ImageURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.ImageURL})
			}
		case types.AudioURLPart:
			if typed.AudioURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.AudioURL})
			}
		case *types.AudioURLPart:
			if typed != nil && typed.AudioURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.AudioURL})
			}
		case types.VideoURLPart:
			if typed.VideoURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.VideoURL})
			}
		case *types.VideoURLPart:
			if typed != nil && typed.VideoURL != "" {
				content = append(content, messageContentBlock{Type: "text", Text: typed.VideoURL})
			}
		}
	}
	if len(content) == 0 {
		return nil
	}
	return content
}

func encodeToolUseBlocks(calls []types.ToolCall) []messageContentBlock {
	if len(calls) == 0 {
		return nil
	}

	blocks := make([]messageContentBlock, 0, len(calls))
	for i := range calls {
		callID := strings.TrimSpace(calls[i].ID)
		callName := strings.TrimSpace(calls[i].Name)
		if callID == "" || callName == "" {
			continue
		}
		blocks = append(blocks, messageContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  callName,
			Input: normalizeToolInput(calls[i].Arguments),
		})
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

func encodeToolResultBlock(msg llm.Message) []messageContentBlock {
	toolCallID := strings.TrimSpace(msg.ToolCallID)
	if toolCallID == "" {
		return nil
	}

	return []messageContentBlock{{
		Type:      "tool_result",
		ToolUseID: toolCallID,
		Content:   encodeToolResultContent(msg.Content),
	}}
}

func encodeToolResultContent(parts types.ContentParts) any {
	if len(parts) == 0 {
		return ""
	}

	texts := make([]string, 0, len(parts))
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if typed.Text != "" {
				texts = append(texts, typed.Text)
			}
		case *types.TextPart:
			if typed != nil && typed.Text != "" {
				texts = append(texts, typed.Text)
			}
		}
	}
	if len(texts) == 0 {
		return ""
	}
	if len(texts) == 1 {
		return texts[0]
	}

	blocks := make([]messageContentBlock, 0, len(texts))
	for i := range texts {
		blocks = append(blocks, messageContentBlock{Type: "text", Text: texts[i]})
	}
	return blocks
}

func contentPartsToText(parts types.ContentParts) string {
	if len(parts) == 0 {
		return ""
	}

	chunks := make([]string, 0, len(parts))
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if typed.Text != "" {
				chunks = append(chunks, typed.Text)
			}
		case *types.TextPart:
			if typed != nil && typed.Text != "" {
				chunks = append(chunks, typed.Text)
			}
		case types.ImageURLPart:
			if typed.ImageURL != "" {
				chunks = append(chunks, typed.ImageURL)
			}
		case *types.ImageURLPart:
			if typed != nil && typed.ImageURL != "" {
				chunks = append(chunks, typed.ImageURL)
			}
		case types.AudioURLPart:
			if typed.AudioURL != "" {
				chunks = append(chunks, typed.AudioURL)
			}
		case *types.AudioURLPart:
			if typed != nil && typed.AudioURL != "" {
				chunks = append(chunks, typed.AudioURL)
			}
		case types.VideoURLPart:
			if typed.VideoURL != "" {
				chunks = append(chunks, typed.VideoURL)
			}
		case *types.VideoURLPart:
			if typed != nil && typed.VideoURL != "" {
				chunks = append(chunks, typed.VideoURL)
			}
		}
	}
	return strings.Join(chunks, "\n")
}

func normalizeToolSchema(schema types.JsonType) types.JsonType {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return schema
}

func normalizeToolInput(input types.JsonType) types.JsonType {
	if input == nil {
		return map[string]any{}
	}
	return input
}

func buildThinkingConfig(effort llm.ThinkingEffort, maxTokens int) *thinkingConfig {
	switch llm.NormalizeThinkingEffort(effort) {
	case llm.ThinkingLow:
		return &thinkingConfig{Type: "enabled", BudgetTokens: capThinkingBudget(thinkingBudgetLow, maxTokens)}
	case llm.ThinkingMedium:
		return &thinkingConfig{Type: "enabled", BudgetTokens: capThinkingBudget(thinkingBudgetMedium, maxTokens)}
	case llm.ThinkingHigh:
		return &thinkingConfig{Type: "enabled", BudgetTokens: capThinkingBudget(thinkingBudgetHigh, maxTokens)}
	default:
		return nil
	}
}

func capThinkingBudget(defaultBudget, maxTokens int) int {
	if maxTokens <= 1 {
		return 1
	}
	if defaultBudget < 1 {
		defaultBudget = 1
	}
	if defaultBudget >= maxTokens {
		return maxTokens - 1
	}
	return defaultBudget
}

func decodeMessageContent(content []messageContentBlock) types.ContentParts {
	if len(content) == 0 {
		return nil
	}

	parts := make(types.ContentParts, 0, len(content))
	for i := range content {
		switch strings.TrimSpace(content[i].Type) {
		case "text":
			if content[i].Text != "" {
				parts = append(parts, types.TextPart{Text: content[i].Text})
			}
		case "thinking":
			// Preserve Signature so it can be round-tripped on the next
			// turn — DeepSeek and Anthropic both reject requests where a
			// historical thinking block has lost its signature.
			if content[i].Thinking != "" || content[i].Signature != "" {
				parts = append(parts, types.ThinkPart{
					Think:     content[i].Thinking,
					Signature: content[i].Signature,
				})
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func decodeToolCalls(content []messageContentBlock) []types.ToolCall {
	if len(content) == 0 {
		return nil
	}

	toolCalls := make([]types.ToolCall, 0, len(content))
	for i := range content {
		if strings.TrimSpace(content[i].Type) != "tool_use" {
			continue
		}
		toolCalls = append(toolCalls, types.ToolCall{
			ID:        strings.TrimSpace(content[i].ID),
			Name:      strings.TrimSpace(content[i].Name),
			Arguments: content[i].Input,
		})
	}
	if len(toolCalls) == 0 {
		return nil
	}
	return toolCalls
}

func normalizeStopReason(stopReason string) string {
	switch strings.TrimSpace(stopReason) {
	case "tool_use":
		return "tool_calls"
	case "end_turn":
		return "stop"
	default:
		return strings.TrimSpace(stopReason)
	}
}

func decodeTokenUsage(usage *messageUsage) types.TokenUsage {
	if usage == nil {
		return types.TokenUsage{}
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	return types.TokenUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              total,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}
}

func mergeUsage(base *types.TokenUsage, usage *messageUsage) *types.TokenUsage {
	if usage == nil {
		return base
	}

	merged := types.TokenUsage{}
	if base != nil {
		merged = *base
	}
	if usage.InputTokens > 0 {
		merged.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		merged.OutputTokens = usage.OutputTokens
	}
	if usage.TotalTokens > 0 {
		merged.TotalTokens = usage.TotalTokens
	} else if merged.InputTokens > 0 || merged.OutputTokens > 0 {
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		merged.CacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		merged.CacheReadInputTokens = usage.CacheReadInputTokens
	}
	return &merged
}

func encodeToolInput(input types.JsonType) string {
	if input == nil {
		return "{}"
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	return trimmed
}

func parseToolInput(raw string) types.JsonType {
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

type messagesRequest struct {
	Model       string          `json:"model"`
	System      string          `json:"system,omitempty"`
	Messages    []messageInput  `json:"messages"`
	Tools       []messageTool   `json:"tools,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens"`
	Stream      bool            `json:"stream,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
}

type messageInput struct {
	Role    string                `json:"role"`
	Content []messageContentBlock `json:"content"`
}

type messageContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Signature string         `json:"signature,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     types.JsonType `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// MarshalJSON ensures that thinking blocks ALWAYS carry the `thinking` and
// `signature` fields on the wire, even when their values are empty strings.
//
// Why this matters: Anthropic's messages API (and DeepSeek's anthropic-compat
// endpoint) require thinking blocks in request bodies to include both fields
// verbatim. Once an assistant turn containing a thinking block is appended
// back into the next-step prompt history, ANY missing field triggers a
// hard 400, e.g.:
//
//	"messages[17].content: missing field `thinking`"
//
// The default struct tags (`omitempty` on Thinking/Signature) drop empty
// values, which is correct for non-thinking block types (text/tool_use/...)
// but wrong for thinking blocks where the schema requires the fields to be
// present. Custom marshaling lets thinking blocks always emit the two fields
// while keeping `omitempty` semantics for all other block types.
func (b messageContentBlock) MarshalJSON() ([]byte, error) {
	type alias messageContentBlock
	if b.Type == "thinking" {
		// Thinking blocks: always emit `thinking` and `signature` (no
		// omitempty), and drop the unrelated tool_use/tool_result fields
		// to keep the wire payload clean.
		wire := struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}{
			Type:      b.Type,
			Thinking:  b.Thinking,
			Signature: b.Signature,
		}
		return json.Marshal(wire)
	}
	return json.Marshal(alias(b))
}

type messageTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema types.JsonType `json:"input_schema"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type messagesResponse struct {
	Content    []messageContentBlock `json:"content"`
	StopReason string                `json:"stop_reason,omitempty"`
	Usage      *messageUsage         `json:"usage,omitempty"`
}

type messageUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	TotalTokens              int `json:"total_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type streamMessageStartEvent struct {
	Type    string `json:"type,omitempty"`
	Message struct {
		Usage *messageUsage `json:"usage,omitempty"`
	} `json:"message"`
}

type streamMessageDeltaEvent struct {
	Type  string `json:"type,omitempty"`
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage *messageUsage `json:"usage,omitempty"`
}

type streamContentBlockStartEvent struct {
	Type         string              `json:"type,omitempty"`
	Index        int                 `json:"index"`
	ContentBlock messageContentBlock `json:"content_block"`
}

type streamContentBlockDeltaEvent struct {
	Type  string `json:"type,omitempty"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type,omitempty"`
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		Signature   string `json:"signature,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

// streamThinking accumulates a thinking block across the chunk events
// (thinking_delta + signature_delta). The completed ThinkPart (with
// Signature attached) is emitted at content_block_stop so downstream
// consumers see one ThinkPart per logical thinking block, not one per
// streaming chunk — that matters for round-trip back to the API:
// DeepSeek's anthropic-compat endpoint hard-rejects requests where
// thinking blocks have lost their signature.
type streamThinking struct {
	think     strings.Builder
	signature strings.Builder
}

type streamContentBlockStopEvent struct {
	Type  string `json:"type,omitempty"`
	Index int    `json:"index"`
}

type streamErrorEvent struct {
	Type  string `json:"type,omitempty"`
	Error struct {
		Type    string `json:"type,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error"`
}

type streamToolUse struct {
	Type            string
	ID              string
	Name            string
	Arguments       strings.Builder
	hasInitialInput bool
	hasPartialInput bool
	emitted         bool
}
