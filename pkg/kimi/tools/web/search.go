package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	searchToolName        = "search_web"
	searchToolDescription = "Search the web through a configured HTTP search service."

	defaultSearchLimit        = 5
	maxSearchLimit            = 20
	defaultSearchTimeout      = 30 * time.Second
	defaultSearchMaxBodyBytes = 1024 * 1024
)

var searchParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Search query text"
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 20,
      "default": 5,
      "description": "Maximum number of results"
    }
  },
  "required": ["query"],
  "additionalProperties": false
}`)

// SearchWeb implements the search_web tool.
type SearchWeb struct {
	SearchServiceURL string
	Client           *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
}

type searchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// NewSearchWeb creates one search_web tool.
func NewSearchWeb(searchServiceURL string, client *http.Client) *SearchWeb {
	return &SearchWeb{
		SearchServiceURL: strings.TrimSpace(searchServiceURL),
		Client:           client,
	}
}

// Name returns the tool name.
func (*SearchWeb) Name() string {
	return searchToolName
}

// Description returns the tool description.
func (*SearchWeb) Description() string {
	return searchToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*SearchWeb) ParameterSchema() json.RawMessage {
	return cloneRawMessage(searchParameterSchema)
}

// Execute sends one search request to the configured service endpoint.
func (t *SearchWeb) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeSearchParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	endpoint := t.serviceURL()
	if endpoint == "" {
		return buildErrorResult(searchToolName, "search_web: search service URL is not configured"), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, t.requestTimeout())
	defer cancel()

	requestBody, err := json.Marshal(map[string]any{
		"query": input.Query,
		"q":     input.Query,
		"limit": input.Limit,
	})
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("search_web: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(runCtx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("search_web: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient().Do(req)
	if err != nil {
		return buildErrorResult(searchToolName, fmt.Sprintf("search_web: request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	bodyText, err := t.readResponseBody(resp.Body)
	if err != nil {
		return buildErrorResult(searchToolName, fmt.Sprintf("search_web: read response body: %v", err)), nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := fmt.Sprintf("search_web: request failed with status %d", resp.StatusCode)
		if hint := firstLine(bodyText); hint != "" {
			message += ": " + hint
		}
		return buildErrorResult(searchToolName, message), nil
	}

	results, err := decodeSearchResults(bodyText)
	if err != nil {
		return buildErrorResult(searchToolName, fmt.Sprintf("search_web: decode response: %v", err)), nil
	}
	if len(results) > input.Limit {
		results = results[:input.Limit]
	}
	return buildResult(searchToolName, formatSearchResults(results), false), nil
}

func decodeSearchParams(raw json.RawMessage) (searchParams, error) {
	input := searchParams{
		Limit: defaultSearchLimit,
	}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return searchParams{}, fmt.Errorf("search_web: decode params: %w", err)
		}
	}

	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return searchParams{}, errors.New("search_web: query is required")
	}
	if input.Limit == 0 {
		input.Limit = defaultSearchLimit
	}
	if input.Limit < 1 || input.Limit > maxSearchLimit {
		return searchParams{}, fmt.Errorf("search_web: limit must be in range [1, %d]", maxSearchLimit)
	}
	return input, nil
}

func (t *SearchWeb) serviceURL() string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.SearchServiceURL)
}

func (t *SearchWeb) httpClient() *http.Client {
	if t != nil && t.Client != nil {
		return t.Client
	}
	return &http.Client{}
}

func (t *SearchWeb) requestTimeout() time.Duration {
	if t != nil && t.Timeout > 0 {
		return t.Timeout
	}
	return defaultSearchTimeout
}

func (t *SearchWeb) maxResponseBytes() int64 {
	if t != nil && t.MaxResponseBytes > 0 {
		return t.MaxResponseBytes
	}
	return defaultSearchMaxBodyBytes
}

func (t *SearchWeb) readResponseBody(reader io.Reader) (string, error) {
	limit := t.maxResponseBytes()
	if limit <= 0 {
		limit = defaultSearchMaxBodyBytes
	}

	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		data = data[:limit]
		data = append(data, []byte("\n...[content-truncated]")...)
	}
	return string(data), nil
}

func decodeSearchResults(bodyText string) ([]searchResult, error) {
	var decoded any
	if err := json.Unmarshal([]byte(bodyText), &decoded); err != nil {
		return nil, err
	}

	items, err := extractResultItems(decoded)
	if err != nil {
		return nil, err
	}

	results := make([]searchResult, 0, len(items))
	for i := range items {
		result, ok := normalizeSearchResult(items[i])
		if !ok {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func extractResultItems(decoded any) ([]any, error) {
	switch typed := decoded.(type) {
	case []any:
		return typed, nil
	case map[string]any:
		for _, key := range []string{"results", "items", "data"} {
			if itemList, ok := typed[key].([]any); ok {
				return itemList, nil
			}
		}
		return nil, errors.New("missing results/items/data array")
	default:
		return nil, errors.New("unsupported response payload")
	}
}

func normalizeSearchResult(item any) (searchResult, bool) {
	payload, ok := item.(map[string]any)
	if !ok {
		return searchResult{}, false
	}

	result := searchResult{
		Title:   firstValue(payload, "title", "name", "headline"),
		URL:     firstValue(payload, "url", "link"),
		Snippet: firstValue(payload, "snippet", "summary", "description", "content", "body"),
	}
	if result.Title == "" && result.URL == "" && result.Snippet == "" {
		return searchResult{}, false
	}
	return result, true
}

func firstValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if text != "" {
				return text
			}
		default:
			text := strings.TrimSpace(fmt.Sprintf("%v", typed))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func formatSearchResults(results []searchResult) string {
	if len(results) == 0 {
		return "No results."
	}

	var builder strings.Builder
	for i := range results {
		entry := results[i]
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = "(untitled)"
		}

		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("%d. %s", i+1, title))

		if entry.URL != "" {
			builder.WriteString("\nURL: ")
			builder.WriteString(strings.TrimSpace(entry.URL))
		}
		if entry.Snippet != "" {
			builder.WriteString("\nSnippet: ")
			builder.WriteString(strings.TrimSpace(entry.Snippet))
		}
	}
	return builder.String()
}
