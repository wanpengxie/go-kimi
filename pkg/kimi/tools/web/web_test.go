package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestFetchURLExecutePlainTextSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("alpha\nbeta"))
	}))
	t.Cleanup(server.Close)

	tool := NewFetchURL(server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": server.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "alpha\nbeta" {
		t.Fatalf("result output = %q, want %q", got, "alpha\\nbeta")
	}
}

func TestFetchURLExecuteHTMLExtractsText(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>ignored title</title>
    <style>.x { color: red; }</style>
    <script>console.log("ignore me")</script>
  </head>
  <body>
    <h1>Article</h1>
    <p>Hello &amp; world.</p>
  </body>
</html>`))
	}))
	t.Cleanup(server.Close)

	tool := NewFetchURL(server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": server.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	output := resultOutputText(t, result)
	for _, want := range []string{"Article", "Hello & world."} {
		if !strings.Contains(output, want) {
			t.Fatalf("result output = %q, want contains %q", output, want)
		}
	}
	if strings.Contains(output, "console.log") {
		t.Fatalf("result output = %q, want script content removed", output)
	}
}

func TestFetchURLExecuteStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	t.Cleanup(server.Close)

	tool := NewFetchURL(server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": server.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "status 502") {
		t.Fatalf("result output = %q, want contains status 502", resultOutputText(t, result))
	}
}

func TestFetchURLExecuteTimeoutReturnsToolError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("slow"))
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	client.Timeout = 20 * time.Millisecond

	tool := NewFetchURL(client)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": server.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "request failed") {
		t.Fatalf("result output = %q, want contains request failed", resultOutputText(t, result))
	}
}

func TestFetchURLExecuteRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	tool := NewFetchURL(nil)
	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "file:///tmp/x",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid url scheme error")
	}
}

func TestFetchURLExecuteTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	longLine := strings.Repeat("x", tools.MaxLineLengthChars+300)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(longLine + "\n" + longLine))
	}))
	t.Cleanup(server.Close)

	tool := NewFetchURL(server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": server.URL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := resultOutputText(t, result)
	if !strings.Contains(output, lineTruncateSuffix) {
		t.Fatalf("result output = %q, want line truncation suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxOutputChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxOutputChars)
	}
}

func TestSearchWebExecuteServiceNotConfigured(t *testing.T) {
	t.Parallel()

	tool := NewSearchWeb("", nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "golang",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "not configured") {
		t.Fatalf("result output = %q, want contains not configured", resultOutputText(t, result))
	}
}

func TestSearchWebExecuteFormatsResults(t *testing.T) {
	t.Parallel()

	type seenRequest struct {
		Query string
		Q     string
		Limit int
	}
	seenCh := make(chan seenRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limit, _ := payload["limit"].(float64)
		seenCh <- seenRequest{
			Query: strings.TrimSpace(asString(payload["query"])),
			Q:     strings.TrimSpace(asString(payload["q"])),
			Limit: int(limit),
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "results": [
    {
      "title": "Result A",
      "url": "https://example.com/a",
      "snippet": "Summary A"
    },
    {
      "name": "Result B",
      "link": "https://example.com/b",
      "description": "Summary B"
    }
  ]
}`))
	}))
	t.Cleanup(server.Close)

	tool := NewSearchWeb(server.URL, server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "golang testing",
		"limit": 2,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	seen := <-seenCh
	if seen.Query != "golang testing" || seen.Q != "golang testing" || seen.Limit != 2 {
		t.Fatalf("request payload = %#v, want query/q=golang testing limit=2", seen)
	}

	output := resultOutputText(t, result)
	for _, want := range []string{
		"1. Result A",
		"URL: https://example.com/a",
		"Snippet: Summary A",
		"2. Result B",
		"URL: https://example.com/b",
		"Snippet: Summary B",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("result output = %q, want contains %q", output, want)
		}
	}
}

func TestSearchWebExecuteUsesDefaultLimit(t *testing.T) {
	t.Parallel()

	limitCh := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limit, _ := payload["limit"].(float64)
		limitCh <- int(limit)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(server.Close)

	tool := NewSearchWeb(server.URL, server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "default limit",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := <-limitCh; got != defaultSearchLimit {
		t.Fatalf("request limit = %d, want %d", got, defaultSearchLimit)
	}
}

func TestSearchWebExecuteRejectsOutOfRangeLimit(t *testing.T) {
	t.Parallel()

	tool := NewSearchWeb("https://example.com/search", nil)
	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "oops",
		"limit": maxSearchLimit + 1,
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want limit range validation error")
	}
}

func TestSearchWebExecuteStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable"))
	}))
	t.Cleanup(server.Close)

	tool := NewSearchWeb(server.URL, server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "golang",
		"limit": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "status 503") {
		t.Fatalf("result output = %q, want contains status 503", resultOutputText(t, result))
	}
}

func TestSearchWebExecuteInvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":`))
	}))
	t.Cleanup(server.Close)

	tool := NewSearchWeb(server.URL, server.Client())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"query": "golang",
		"limit": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "decode response") {
		t.Fatalf("result output = %q, want contains decode response", resultOutputText(t, result))
	}
}

func mustParams(t *testing.T, input any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func resultOutputText(t *testing.T, result types.ToolResult) string {
	t.Helper()
	output, ok := result.Value.Value.(string)
	if !ok {
		t.Fatalf("result output type = %T, want string", result.Value.Value)
	}
	return output
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
