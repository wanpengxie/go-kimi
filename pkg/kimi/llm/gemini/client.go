package gemini

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
	"net/url"
	"strings"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	defaultBaseURL        = "https://generativelanguage.googleapis.com"
	defaultAPIVersionPath = "/v1beta"
	defaultModel          = "gemini-2.0-flash"
	defaultRequestTO      = 120 * time.Second
	defaultInitialBackoff = 300 * time.Millisecond
	defaultMaxAttempts    = 3
	streamScannerMaxSize  = 4 * 1024 * 1024
)

const (
	thinkingBudgetLow    = 512
	thinkingBudgetMedium = 1024
	thinkingBudgetHigh   = 2048
)

// GeminiClient calls the Google Gemini generateContent API.
type GeminiClient struct {
	apiKey         string
	baseURL        string
	model          string
	httpClient     *http.Client
	thinkingEffort llm.ThinkingEffort
	maxAttempts    int
	initialBackoff time.Duration
}

var _ llm.ChatProvider = (*GeminiClient)(nil)
var _ llm.ThinkingProvider = (*GeminiClient)(nil)

// NewGeminiClient creates a Gemini HTTP client with sensible defaults.
func NewGeminiClient(apiKey, baseURL, model string) *GeminiClient {
	normalizedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalizedBaseURL == "" {
		normalizedBaseURL = defaultBaseURL
	}
	normalizedModel := strings.TrimSpace(model)
	if normalizedModel == "" {
		normalizedModel = defaultModel
	}

	return &GeminiClient{
		apiKey:         strings.TrimSpace(apiKey),
		baseURL:        normalizedBaseURL,
		model:          normalizedModel,
		httpClient:     &http.Client{Timeout: defaultRequestTO},
		thinkingEffort: llm.ThinkingOff,
		maxAttempts:    defaultMaxAttempts,
		initialBackoff: defaultInitialBackoff,
	}
}

// NewGeminiClientFromConfig builds a Gemini client from provider/model config.
func NewGeminiClientFromConfig(provider config.LLMProvider, model config.LLMModel) *GeminiClient {
	return NewGeminiClient(provider.APIKey.Raw(), provider.BaseURL, model.Name)
}

// ModelName returns the configured model identifier.
func (c *GeminiClient) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// WithModel returns a cloned provider with updated model identifier.
func (c *GeminiClient) WithModel(model string) llm.ChatProvider {
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
func (c *GeminiClient) WithThinking(effort llm.ThinkingEffort) llm.ChatProvider {
	if c == nil {
		return c
	}
	cloned := *c
	cloned.thinkingEffort = llm.NormalizeThinkingEffort(effort)
	return &cloned
}

// Chat performs a non-streaming generateContent API call.
func (c *GeminiClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if c == nil {
		return nil, errors.New("gemini client: nil")
	}

	payload := c.buildGenerateContentRequest(req)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gemini chat: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var decoded generateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("gemini chat: decode response: %w", err)
	}
	if len(decoded.Candidates) == 0 {
		return nil, errors.New("gemini chat: missing candidates in response")
	}

	firstCandidate := decoded.Candidates[0]
	toolCalls := decodeCandidateToolCalls(firstCandidate.Content, firstCandidate.Index)

	response := &llm.ChatResponse{
		Content:    decodeCandidateContent(firstCandidate.Content),
		ToolCalls:  toolCalls,
		Usage:      decodeTokenUsage(decoded.UsageMetadata),
		StopReason: normalizeStopReason(firstCandidate.FinishReason, len(toolCalls) > 0),
	}
	return response, nil
}

// ChatStream performs a streaming streamGenerateContent API call and returns SSE events.
func (c *GeminiClient) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	if c == nil {
		return nil, errors.New("gemini client: nil")
	}

	payload := c.buildGenerateContentRequest(req)
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gemini chat stream: marshal request: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, rawPayload, true)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.ChatEvent)
	go c.consumeStream(ctx, resp.Body, events)
	return events, nil
}

func (c *GeminiClient) consumeStream(ctx context.Context, body io.ReadCloser, out chan<- llm.ChatEvent) {
	defer close(out)
	defer func() {
		_ = body.Close()
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxSize)

	emittedToolCalls := map[string]struct{}{}
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
		stop := c.handleStreamFrame(ctx, out, strings.TrimSpace(eventName), frameData, emittedToolCalls, &usage, &doneEmitted)
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
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("gemini chat stream: read stream: %w", err), Done: true})
		return
	}

	if handleFrame() {
		return
	}

	if !doneEmitted {
		c.emitEvent(ctx, out, llm.ChatEvent{Usage: usage, Done: true})
	}
}

func (c *GeminiClient) handleStreamFrame(
	ctx context.Context,
	out chan<- llm.ChatEvent,
	eventName string,
	data string,
	emittedToolCalls map[string]struct{},
	usage **types.TokenUsage,
	doneEmitted *bool,
) bool {
	trimmedData := strings.TrimSpace(data)
	if trimmedData == "" {
		return false
	}
	if trimmedData == "[DONE]" {
		c.emitEvent(ctx, out, llm.ChatEvent{Usage: *usage, Done: true})
		*doneEmitted = true
		return true
	}
	if strings.EqualFold(eventName, "error") {
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("gemini chat stream: %s", trimmedData), Done: true})
		*doneEmitted = true
		return true
	}

	var chunk generateContentResponse
	if err := json.Unmarshal([]byte(trimmedData), &chunk); err != nil {
		c.emitEvent(ctx, out, llm.ChatEvent{Err: fmt.Errorf("gemini chat stream: decode chunk: %w", err), Done: true})
		*doneEmitted = true
		return true
	}

	*usage = mergeUsage(*usage, chunk.UsageMetadata)

	for _, candidate := range chunk.Candidates {
		for partIndex, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				event := llm.ChatEvent{}
				if part.Thought {
					event.Delta = types.ThinkPart{Think: text}
				} else {
					event.Delta = types.TextPart{Text: text}
				}
				if !c.emitEvent(ctx, out, event) {
					return true
				}
			}

			if part.FunctionCall == nil {
				continue
			}
			functionName := strings.TrimSpace(part.FunctionCall.Name)
			if functionName == "" {
				continue
			}

			toolKey := streamToolKey(candidate.Index, partIndex, part.FunctionCall)
			if _, exists := emittedToolCalls[toolKey]; exists {
				continue
			}
			emittedToolCalls[toolKey] = struct{}{}

			toolCallID := strings.TrimSpace(part.FunctionCall.ID)
			if toolCallID == "" {
				toolCallID = fmt.Sprintf("tool_call_%d_%d", candidate.Index, partIndex)
			}

			event := llm.ChatEvent{
				ToolCall: &types.ToolCall{
					ID:        toolCallID,
					Name:      functionName,
					Arguments: normalizeToolCallArguments(part.FunctionCall.Args),
				},
			}
			if !c.emitEvent(ctx, out, event) {
				return true
			}
		}
	}

	return false
}

func (c *GeminiClient) emitEvent(ctx context.Context, out chan<- llm.ChatEvent, event llm.ChatEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func (c *GeminiClient) doRequestWithRetry(ctx context.Context, payload []byte, stream bool) (*http.Response, error) {
	attempts := c.maxAttempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := c.initialBackoff
	if backoff <= 0 {
		backoff = defaultInitialBackoff
	}

	endpoint := c.endpointURL(stream)
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("gemini request: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}
		if c.apiKey != "" {
			req.Header.Set("x-goog-api-key", c.apiKey)
		}

		client := c.httpClientForMode(stream)
		resp, err := client.Do(req)
		if err != nil {
			if attempt < attempts && isRetryableTransportError(err) {
				lastErr = fmt.Errorf("gemini request: transport error: %w", err)
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return nil, sleepErr
				}
				backoff *= 2
				continue
			}
			return nil, fmt.Errorf("gemini request: do request: %w", err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		statusErr := fmt.Errorf(
			"gemini request: status %d: %s",
			resp.StatusCode,
			strings.TrimSpace(readBodyForError(resp.Body)),
		)
		discardAndClose(resp.Body)

		if attempt < attempts && isRetryableStatusCode(resp.StatusCode) {
			lastErr = statusErr
			if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
				return nil, sleepErr
			}
			backoff *= 2
			continue
		}
		return nil, statusErr
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("gemini request: exhausted retries")
}

func (c *GeminiClient) httpClientForMode(stream bool) *http.Client {
	if c.httpClient == nil {
		if stream {
			return &http.Client{Timeout: 0}
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

func (c *GeminiClient) endpointURL(stream bool) string {
	baseURL := strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = defaultAPIVersionPath
	} else if !strings.HasSuffix(path, defaultAPIVersionPath) && !strings.Contains(path, defaultAPIVersionPath+"/") {
		path += defaultAPIVersionPath
	}

	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	path = strings.TrimRight(path, "/") + "/models/" + url.PathEscape(c.model) + ":" + action
	parsed.Path = path

	query := parsed.Query()
	if stream {
		query.Set("alt", "sse")
	}
	parsed.RawQuery = query.Encode()

	return parsed.String()
}

func (c *GeminiClient) buildGenerateContentRequest(req llm.ChatRequest) generateContentRequest {
	contents, systemInstruction := buildContentsAndSystem(req.Messages)
	request := generateContentRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             encodeTools(req.Tools),
	}

	generationConfig := &generationConfig{}
	hasGenerationConfig := false
	if req.Temperature != 0 {
		temperature := req.Temperature
		generationConfig.Temperature = &temperature
		hasGenerationConfig = true
	}
	if req.MaxTokens > 0 {
		generationConfig.MaxOutputTokens = req.MaxTokens
		hasGenerationConfig = true
	}
	if thinking := buildThinkingConfig(c.thinkingEffort, req.MaxTokens); thinking != nil {
		generationConfig.ThinkingConfig = thinking
		hasGenerationConfig = true
	}
	if hasGenerationConfig {
		request.GenerationConfig = generationConfig
	}

	return request
}

func buildContentsAndSystem(messages []llm.Message) ([]contentInput, *contentInput) {
	contents := make([]contentInput, 0, len(messages))
	systemChunks := make([]string, 0, 2)
	toolNameByID := map[string]string{}

	appendContent := func(role string, parts []contentPart) {
		if len(parts) == 0 {
			return
		}
		normalizedRole := normalizeContentRole(role)
		lastIndex := len(contents) - 1
		if lastIndex >= 0 && contents[lastIndex].Role == normalizedRole {
			contents[lastIndex].Parts = append(contents[lastIndex].Parts, parts...)
			return
		}
		contents = append(contents, contentInput{Role: normalizedRole, Parts: parts})
	}

	for i := range messages {
		message := messages[i]
		role := strings.ToLower(strings.TrimSpace(message.Role))

		switch role {
		case "system":
			if chunk := contentPartsToText(message.Content); chunk != "" {
				systemChunks = append(systemChunks, chunk)
			}
		case "assistant", "model":
			for j := range message.ToolCalls {
				callID := strings.TrimSpace(message.ToolCalls[j].ID)
				callName := strings.TrimSpace(message.ToolCalls[j].Name)
				if callID == "" || callName == "" {
					continue
				}
				toolNameByID[callID] = callName
			}

			parts := encodeRegularParts(message.Content)
			parts = append(parts, encodeFunctionCallParts(message.ToolCalls)...)
			appendContent("model", parts)
		case "tool":
			appendContent("user", encodeFunctionResponseParts(message, toolNameByID))
		default:
			appendContent("user", encodeRegularParts(message.Content))
		}
	}

	if len(systemChunks) == 0 {
		return contents, nil
	}

	return contents, &contentInput{
		Parts: []contentPart{{Text: strings.Join(systemChunks, "\n\n")}},
	}
}

func normalizeContentRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "model":
		return "model"
	default:
		return "user"
	}
}

func encodeTools(tools []llm.ToolDefinition) []toolDefinition {
	if len(tools) == 0 {
		return nil
	}

	declarations := make([]functionDeclaration, 0, len(tools))
	for i := range tools {
		name := strings.TrimSpace(tools[i].Name)
		if name == "" {
			continue
		}
		declarations = append(declarations, functionDeclaration{
			Name:        name,
			Description: tools[i].Description,
			Parameters:  normalizeToolSchema(tools[i].Parameters),
		})
	}
	if len(declarations) == 0 {
		return nil
	}

	return []toolDefinition{{FunctionDeclarations: declarations}}
}

func encodeRegularParts(parts types.ContentParts) []contentPart {
	if len(parts) == 0 {
		return nil
	}

	encoded := make([]contentPart, 0, len(parts))
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if strings.TrimSpace(typed.Text) != "" {
				encoded = append(encoded, contentPart{Text: typed.Text})
			}
		case *types.TextPart:
			if typed != nil && strings.TrimSpace(typed.Text) != "" {
				encoded = append(encoded, contentPart{Text: typed.Text})
			}
		case types.ImageURLPart:
			if strings.TrimSpace(typed.ImageURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.ImageURL})
			}
		case *types.ImageURLPart:
			if typed != nil && strings.TrimSpace(typed.ImageURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.ImageURL})
			}
		case types.AudioURLPart:
			if strings.TrimSpace(typed.AudioURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.AudioURL})
			}
		case *types.AudioURLPart:
			if typed != nil && strings.TrimSpace(typed.AudioURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.AudioURL})
			}
		case types.VideoURLPart:
			if strings.TrimSpace(typed.VideoURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.VideoURL})
			}
		case *types.VideoURLPart:
			if typed != nil && strings.TrimSpace(typed.VideoURL) != "" {
				encoded = append(encoded, contentPart{Text: typed.VideoURL})
			}
		}
	}
	if len(encoded) == 0 {
		return nil
	}
	return encoded
}

func encodeFunctionCallParts(calls []types.ToolCall) []contentPart {
	if len(calls) == 0 {
		return nil
	}

	parts := make([]contentPart, 0, len(calls))
	for i := range calls {
		name := strings.TrimSpace(calls[i].Name)
		if name == "" {
			continue
		}
		call := &functionCall{
			ID:   strings.TrimSpace(calls[i].ID),
			Name: name,
			Args: normalizeFunctionArgs(calls[i].Arguments),
		}
		parts = append(parts, contentPart{FunctionCall: call})
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func encodeFunctionResponseParts(message llm.Message, toolNameByID map[string]string) []contentPart {
	toolCallID := strings.TrimSpace(message.ToolCallID)
	if toolCallID == "" {
		return nil
	}

	functionName := strings.TrimSpace(toolNameByID[toolCallID])
	if functionName == "" {
		functionName = toolCallID
	}

	return []contentPart{{
		FunctionResponse: &functionResponse{
			Name:     functionName,
			Response: normalizeFunctionResponse(message.Content),
		},
	}}
}

func contentPartsToText(parts types.ContentParts) string {
	if len(parts) == 0 {
		return ""
	}

	chunks := make([]string, 0, len(parts))
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if strings.TrimSpace(typed.Text) != "" {
				chunks = append(chunks, typed.Text)
			}
		case *types.TextPart:
			if typed != nil && strings.TrimSpace(typed.Text) != "" {
				chunks = append(chunks, typed.Text)
			}
		case types.ImageURLPart:
			if strings.TrimSpace(typed.ImageURL) != "" {
				chunks = append(chunks, typed.ImageURL)
			}
		case *types.ImageURLPart:
			if typed != nil && strings.TrimSpace(typed.ImageURL) != "" {
				chunks = append(chunks, typed.ImageURL)
			}
		case types.AudioURLPart:
			if strings.TrimSpace(typed.AudioURL) != "" {
				chunks = append(chunks, typed.AudioURL)
			}
		case *types.AudioURLPart:
			if typed != nil && strings.TrimSpace(typed.AudioURL) != "" {
				chunks = append(chunks, typed.AudioURL)
			}
		case types.VideoURLPart:
			if strings.TrimSpace(typed.VideoURL) != "" {
				chunks = append(chunks, typed.VideoURL)
			}
		case *types.VideoURLPart:
			if typed != nil && strings.TrimSpace(typed.VideoURL) != "" {
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

func normalizeFunctionArgs(args types.JsonType) types.JsonType {
	return normalizeJSONObject(args)
}

func normalizeFunctionResponse(parts types.ContentParts) types.JsonType {
	text := strings.TrimSpace(contentPartsToText(parts))
	if text == "" {
		return map[string]any{}
	}

	var decoded types.JsonType
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		if object, ok := decoded.(map[string]any); ok {
			return object
		}
		return map[string]any{"value": decoded}
	}

	return map[string]any{"content": text}
}

func buildThinkingConfig(effort llm.ThinkingEffort, maxOutputTokens int) *thinkingConfig {
	switch llm.NormalizeThinkingEffort(effort) {
	case llm.ThinkingLow:
		return &thinkingConfig{ThinkingBudget: capThinkingBudget(thinkingBudgetLow, maxOutputTokens)}
	case llm.ThinkingMedium:
		return &thinkingConfig{ThinkingBudget: capThinkingBudget(thinkingBudgetMedium, maxOutputTokens)}
	case llm.ThinkingHigh:
		return &thinkingConfig{ThinkingBudget: capThinkingBudget(thinkingBudgetHigh, maxOutputTokens)}
	default:
		return nil
	}
}

func capThinkingBudget(defaultBudget, maxOutputTokens int) int {
	if defaultBudget < 1 {
		defaultBudget = 1
	}
	if maxOutputTokens <= 0 {
		return defaultBudget
	}
	if maxOutputTokens <= 1 {
		return 1
	}
	if defaultBudget >= maxOutputTokens {
		return maxOutputTokens - 1
	}
	return defaultBudget
}

func decodeCandidateContent(content contentInput) types.ContentParts {
	if len(content.Parts) == 0 {
		return nil
	}

	parts := make(types.ContentParts, 0, len(content.Parts))
	for i := range content.Parts {
		text := strings.TrimSpace(content.Parts[i].Text)
		if text == "" {
			continue
		}
		if content.Parts[i].Thought {
			parts = append(parts, types.ThinkPart{Think: text})
			continue
		}
		parts = append(parts, types.TextPart{Text: text})
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func decodeCandidateToolCalls(content contentInput, candidateIndex int) []types.ToolCall {
	if len(content.Parts) == 0 {
		return nil
	}

	toolCalls := make([]types.ToolCall, 0, len(content.Parts))
	for partIndex := range content.Parts {
		call := content.Parts[partIndex].FunctionCall
		if call == nil {
			continue
		}
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = fmt.Sprintf("tool_call_%d_%d", candidateIndex, partIndex)
		}

		toolCalls = append(toolCalls, types.ToolCall{
			ID:        callID,
			Name:      name,
			Arguments: normalizeToolCallArguments(call.Args),
		})
	}
	if len(toolCalls) == 0 {
		return nil
	}
	return toolCalls
}

func normalizeToolCallArguments(args types.JsonType) types.JsonType {
	return normalizeJSONObject(args)
}

func normalizeStopReason(stopReason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}

	switch strings.ToUpper(strings.TrimSpace(stopReason)) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(stopReason))
	}
}

func decodeTokenUsage(usage *usageMetadata) types.TokenUsage {
	if usage == nil {
		return types.TokenUsage{}
	}

	total := usage.TotalTokenCount
	if total == 0 {
		total = usage.PromptTokenCount + usage.CandidatesTokenCount
	}
	return types.TokenUsage{
		InputTokens:  usage.PromptTokenCount,
		OutputTokens: usage.CandidatesTokenCount,
		TotalTokens:  total,
	}
}

func mergeUsage(base *types.TokenUsage, usage *usageMetadata) *types.TokenUsage {
	if usage == nil {
		return base
	}

	merged := types.TokenUsage{}
	if base != nil {
		merged = *base
	}
	if usage.PromptTokenCount > 0 {
		merged.InputTokens = usage.PromptTokenCount
	}
	if usage.CandidatesTokenCount > 0 {
		merged.OutputTokens = usage.CandidatesTokenCount
	}
	if usage.TotalTokenCount > 0 {
		merged.TotalTokens = usage.TotalTokenCount
	} else if merged.InputTokens > 0 || merged.OutputTokens > 0 {
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	return &merged
}

func normalizeJSONValue(value types.JsonType) types.JsonType {
	if value == nil {
		return nil
	}

	if raw, ok := value.(string); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil
		}
		var decoded types.JsonType
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			return decoded
		}
		return trimmed
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized types.JsonType
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return value
	}
	return normalized
}

func normalizeJSONObject(value types.JsonType) map[string]any {
	normalized := normalizeJSONValue(value)
	if normalized == nil {
		return map[string]any{}
	}
	if object, ok := normalized.(map[string]any); ok {
		return object
	}
	return map[string]any{"value": normalized}
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

func streamToolKey(candidateIndex, partIndex int, call *functionCall) string {
	if call == nil {
		return fmt.Sprintf("%d:%d", candidateIndex, partIndex)
	}
	if callID := strings.TrimSpace(call.ID); callID != "" {
		return "id:" + callID
	}
	return fmt.Sprintf("%d:%d:%s", candidateIndex, partIndex, strings.TrimSpace(call.Name))
}

type generateContentRequest struct {
	Contents          []contentInput    `json:"contents"`
	SystemInstruction *contentInput     `json:"systemInstruction,omitempty"`
	Tools             []toolDefinition  `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxOutputTokens int             `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

type contentInput struct {
	Role  string        `json:"role,omitempty"`
	Parts []contentPart `json:"parts"`
}

type contentPart struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
}

type functionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name,omitempty"`
	Args types.JsonType `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response types.JsonType `json:"response,omitempty"`
}

type toolDefinition struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  types.JsonType `json:"parameters,omitempty"`
}

type generateContentResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
}

type candidate struct {
	Index        int          `json:"index,omitempty"`
	Content      contentInput `json:"content"`
	FinishReason string       `json:"finishReason,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}
