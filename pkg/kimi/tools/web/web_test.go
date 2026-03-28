package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

	const fetchURL = "http://docs.example/plain"
	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.URL.String(); got != fetchURL {
				t.Fatalf("request URL = %q, want %q", got, fetchURL)
			}
			return mockHTTPResponse(http.StatusOK, "text/plain; charset=utf-8", "alpha\nbeta"), nil
		}),
	})
	tool.resolver = staticHostResolver{
		"docs.example": {
			{IP: net.ParseIP("93.184.216.34")},
		},
	}

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": fetchURL,
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

	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return mockHTTPResponse(http.StatusOK, "text/html; charset=utf-8", `<!doctype html>
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
</html>`), nil
		}),
	})

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://93.184.216.34/article",
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

	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return mockHTTPResponse(http.StatusBadGateway, "", "upstream unavailable"), nil
		}),
	})
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://93.184.216.34/status",
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

	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			select {
			case <-time.After(120 * time.Millisecond):
				return mockHTTPResponse(http.StatusOK, "text/plain", "slow"), nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}),
	})
	tool.Timeout = 20 * time.Millisecond

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://93.184.216.34/slow",
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

func TestFetchURLExecuteRejectsUnexpectedField(t *testing.T) {
	t.Parallel()

	tool := NewFetchURL(nil)
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url":        "http://93.184.216.34/article",
		"unexpected": true,
	})); err == nil {
		t.Fatal("Execute(unexpected field) error = nil, want validation error")
	}
}

func TestFetchURLExecuteRejectsBlockedTargetHost(t *testing.T) {
	t.Parallel()

	tool := NewFetchURL(nil)

	testCases := []string{
		"http://localhost/path",
		"http://127.0.0.1/path",
		"http://10.0.0.8/path",
		"http://169.254.169.254/latest/meta-data",
		"http://0.0.0.0/path",
		"http://224.0.0.1/path",
		"http://[::1]/path",
		"http://[fc00::1]/path",
		"http://[::]/path",
		"http://[ff02::1]/path",
	}

	for i := range testCases {
		_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
			"url": testCases[i],
		}))
		if err == nil {
			t.Fatalf("Execute(%q) error = nil, want blocked host error", testCases[i])
		}
	}
}

func TestFetchURLExecuteRejectsHostnameResolvingToBlockedIP(t *testing.T) {
	t.Parallel()

	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("RoundTrip should not be called when target host is blocked")
			return nil, nil
		}),
	})
	tool.resolver = staticHostResolver{
		"internal.example": {
			{IP: net.ParseIP("10.0.0.10")},
		},
	}

	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://internal.example/private",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want blocked host error")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("Execute() error = %v, want contains blocked address", err)
	}
}

func TestFetchURLExecuteRejectsRedirectToBlockedHost(t *testing.T) {
	t.Parallel()

	const (
		initialURL  = "http://public.example/start"
		redirectURL = "http://127.0.0.1/private"
	)

	requestCount := 0
	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			if requestCount > 1 {
				t.Fatalf("redirect target should be blocked before second round trip, got request to %q", req.URL.String())
			}
			if got := req.URL.String(); got != initialURL {
				t.Fatalf("request URL = %q, want %q", got, initialURL)
			}
			resp := mockHTTPResponse(http.StatusFound, "text/plain", "redirect")
			resp.Header.Set("Location", redirectURL)
			resp.Request = req
			return resp, nil
		}),
	})
	tool.resolver = staticHostResolver{
		"public.example": {
			{IP: net.ParseIP("93.184.216.34")},
		},
	}

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": initialURL,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	output := resultOutputText(t, result)
	if !strings.Contains(output, "127.0.0.1") || !strings.Contains(output, "not allowed") {
		t.Fatalf("result output = %q, want contains blocked redirect address", output)
	}
}

func TestFetchURLExecuteRejectsDNSRebindingInDialContext(t *testing.T) {
	t.Parallel()

	lookupCount := 0
	tool := NewFetchURL(&http.Client{
		Transport: &http.Transport{
			Proxy: nil,
		},
	})
	tool.resolver = hostLookupResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "rebind.example" {
			return nil, fmt.Errorf("unexpected host: %s", host)
		}
		lookupCount++
		if lookupCount == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://rebind.example/path",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if lookupCount < 2 {
		t.Fatalf("resolver lookup count = %d, want >= 2 to cover preflight + dial context", lookupCount)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, "127.0.0.1") || !strings.Contains(output, "blocked address") {
		t.Fatalf("result output = %q, want contains blocked rebinding address", output)
	}
}

func TestFetchURLExecuteTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	longLine := strings.Repeat("x", tools.MaxLineLengthChars+300)
	tool := NewFetchURL(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return mockHTTPResponse(http.StatusOK, "text/plain", longLine+"\n"+longLine), nil
		}),
	})
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"url": "http://93.184.216.34/long",
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type hostLookupResolverFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

func (fn hostLookupResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return fn(ctx, host)
}

type staticHostResolver map[string][]net.IPAddr

func (s staticHostResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	addresses, ok := s[host]
	if !ok {
		return nil, fmt.Errorf("host not found: %s", host)
	}
	return addresses, nil
}

func mockHTTPResponse(statusCode int, contentType, body string) *http.Response {
	header := make(http.Header)
	if strings.TrimSpace(contentType) != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
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
