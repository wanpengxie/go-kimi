package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	fetchToolName        = "fetch_url"
	fetchToolDescription = "Fetch one URL by HTTP GET and return extracted text content."

	defaultFetchTimeout         = 30 * time.Second
	defaultFetchDialTimeout     = 10 * time.Second
	defaultFetchMaxContentBytes = 2 * 1024 * 1024
	defaultChromeUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var fetchParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "HTTP or HTTPS URL to fetch"
    }
  },
  "required": ["url"],
  "additionalProperties": false
}`)

var (
	scriptTagPattern      = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleTagPattern       = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlCommentPattern    = regexp.MustCompile(`(?is)<!--.*?-->`)
	htmlTagPattern        = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlWhitespacePattern = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// FetchURL implements the fetch_url web tool.
type FetchURL struct {
	Client          *http.Client
	UserAgent       string
	Timeout         time.Duration
	MaxContentBytes int64

	resolver hostLookupResolver
}

type fetchParams struct {
	URL string `json:"url"`

	parsedURL *url.URL
}

type hostLookupResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// NewFetchURL creates a fetch_url tool.
func NewFetchURL(client *http.Client) *FetchURL {
	return &FetchURL{Client: client}
}

// Name returns the tool name.
func (*FetchURL) Name() string {
	return fetchToolName
}

// Description returns the tool description.
func (*FetchURL) Description() string {
	return fetchToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*FetchURL) ParameterSchema() json.RawMessage {
	return cloneRawMessage(fetchParameterSchema)
}

// Execute fetches one URL and extracts textual content.
func (t *FetchURL) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeFetchParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, t.requestTimeout())
	defer cancel()

	resolver := t.hostResolver()
	if err := validateFetchTargetHost(runCtx, input.parsedURL, resolver); err != nil {
		return types.ToolResult{}, err
	}

	req, err := http.NewRequestWithContext(runCtx, http.MethodGet, input.URL, nil)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("fetch_url: build request: %w", err)
	}
	req.Header.Set("User-Agent", t.userAgent())

	resp, err := t.httpClientWithNetworkGuards(resolver).Do(req)
	if err != nil {
		return buildErrorResult(fetchToolName, fmt.Sprintf("fetch_url: request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	bodyText, err := t.readResponseBody(resp.Body)
	if err != nil {
		return buildErrorResult(fetchToolName, fmt.Sprintf("fetch_url: read response body: %v", err)), nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := fmt.Sprintf("fetch_url: request failed with status %d", resp.StatusCode)
		if hint := firstLine(bodyText); hint != "" {
			message += ": " + hint
		}
		return buildErrorResult(fetchToolName, message), nil
	}

	content := decodeFetchedContent(bodyText, resp.Header.Get("Content-Type"))
	return buildResult(fetchToolName, content, false), nil
}

func decodeFetchParams(raw json.RawMessage) (fetchParams, error) {
	input := fetchParams{}

	text := strings.TrimSpace(string(raw))
	if text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return fetchParams{}, fmt.Errorf("fetch_url: decode params: %w", err)
		}
	}

	input.URL = strings.TrimSpace(input.URL)
	if input.URL == "" {
		return fetchParams{}, errors.New("fetch_url: url is required")
	}

	parsed, err := url.ParseRequestURI(input.URL)
	if err != nil {
		return fetchParams{}, fmt.Errorf("fetch_url: invalid url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return fetchParams{}, errors.New("fetch_url: url scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return fetchParams{}, errors.New("fetch_url: url host is required")
	}
	input.parsedURL = parsed
	return input, nil
}

func validateFetchTargetHost(ctx context.Context, parsedURL *url.URL, resolver hostLookupResolver) error {
	if parsedURL == nil {
		return errors.New("fetch_url: invalid url host")
	}

	host := strings.TrimSpace(parsedURL.Hostname())
	if host == "" {
		return errors.New("fetch_url: url host is required")
	}
	if strings.EqualFold(host, "localhost") {
		return errors.New("fetch_url: localhost is not allowed")
	}

	if ip := parseHostIP(host); ip != nil {
		if isBlockedFetchIP(ip) {
			return fmt.Errorf("fetch_url: target address %s is not allowed", ip.String())
		}
		return nil
	}

	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("fetch_url: resolve host %q: %w", host, err)
	}
	if len(resolved) == 0 {
		return fmt.Errorf("fetch_url: resolve host %q: no IP addresses found", host)
	}

	for i := range resolved {
		if isBlockedFetchIP(resolved[i].IP) {
			return fmt.Errorf("fetch_url: target host %q resolves to blocked address %s", host, resolved[i].IP.String())
		}
	}
	return nil
}

func parseHostIP(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	if idx := strings.LastIndex(host, "%"); idx > 0 {
		if ip := net.ParseIP(host[:idx]); ip != nil {
			return ip
		}
	}
	return nil
}

func isBlockedFetchIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

func (t *FetchURL) httpClient() *http.Client {
	if t != nil && t.Client != nil {
		return t.Client
	}
	return &http.Client{}
}

func (t *FetchURL) hostResolver() hostLookupResolver {
	if t != nil && t.resolver != nil {
		return t.resolver
	}
	return net.DefaultResolver
}

func (t *FetchURL) httpClientWithNetworkGuards(resolver hostLookupResolver) *http.Client {
	base := t.httpClient()
	client := *base
	client.CheckRedirect = wrapFetchCheckRedirect(base.CheckRedirect, resolver)

	transport, ok := cloneFetchTransport(base.Transport)
	if !ok {
		return &client
	}

	transport.DialContext = newFetchDialContext(resolver)
	client.Transport = transport
	return &client
}

func wrapFetchCheckRedirect(
	base func(req *http.Request, via []*http.Request) error,
	resolver hostLookupResolver,
) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if err := validateFetchTargetHost(req.Context(), req.URL, resolver); err != nil {
			return err
		}
		if base != nil {
			return base(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

func cloneFetchTransport(base http.RoundTripper) (*http.Transport, bool) {
	if base == nil {
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, false
		}
		return defaultTransport.Clone(), true
	}

	transport, ok := base.(*http.Transport)
	if !ok {
		return nil, false
	}
	return transport.Clone(), true
}

func newFetchDialContext(
	resolver hostLookupResolver,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: defaultFetchDialTimeout,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("fetch_url: split dial address %q: %w", addr, err)
		}

		ip, err := resolveFetchDialIP(ctx, resolver, host)
		if err != nil {
			return nil, err
		}

		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
}

func resolveFetchDialIP(ctx context.Context, resolver hostLookupResolver, host string) (net.IP, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("fetch_url: empty dial host")
	}
	if strings.EqualFold(host, "localhost") {
		return nil, errors.New("fetch_url: localhost is not allowed")
	}

	if ip := parseHostIP(host); ip != nil {
		if isBlockedFetchIP(ip) {
			return nil, fmt.Errorf("fetch_url: target address %s is not allowed", ip.String())
		}
		return ip, nil
	}

	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("fetch_url: resolve host %q: %w", host, err)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("fetch_url: resolve host %q: no IP addresses found", host)
	}

	for i := range resolved {
		if isBlockedFetchIP(resolved[i].IP) {
			return nil, fmt.Errorf("fetch_url: target host %q resolves to blocked address %s", host, resolved[i].IP.String())
		}
	}
	return resolved[0].IP, nil
}

func (t *FetchURL) userAgent() string {
	if t == nil {
		return defaultChromeUserAgent
	}
	value := strings.TrimSpace(t.UserAgent)
	if value == "" {
		return defaultChromeUserAgent
	}
	return value
}

func (t *FetchURL) requestTimeout() time.Duration {
	if t != nil && t.Timeout > 0 {
		return t.Timeout
	}
	return defaultFetchTimeout
}

func (t *FetchURL) maxContentBytes() int64 {
	if t != nil && t.MaxContentBytes > 0 {
		return t.MaxContentBytes
	}
	return defaultFetchMaxContentBytes
}

func (t *FetchURL) readResponseBody(reader io.Reader) (string, error) {
	limit := t.maxContentBytes()
	if limit <= 0 {
		limit = defaultFetchMaxContentBytes
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

func decodeFetchedContent(bodyText, contentType string) string {
	mediaType := normalizedMediaType(contentType)
	switch {
	case mediaType == "text/plain", mediaType == "text/markdown":
		return bodyText
	case mediaType == "text/html", mediaType == "application/xhtml+xml":
		return extractTextFromHTML(bodyText)
	case strings.HasPrefix(mediaType, "text/"):
		return bodyText
	case looksLikeHTML(bodyText):
		return extractTextFromHTML(bodyText)
	default:
		return bodyText
	}
}

func normalizedMediaType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		if idx := strings.Index(contentType, ";"); idx >= 0 {
			contentType = contentType[:idx]
		}
		return strings.ToLower(strings.TrimSpace(contentType))
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func looksLikeHTML(text string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "<html") ||
		strings.Contains(trimmed, "<body") ||
		strings.Contains(trimmed, "<div") ||
		strings.Contains(trimmed, "<p")
}

func extractTextFromHTML(source string) string {
	cleaned := scriptTagPattern.ReplaceAllString(source, "\n")
	cleaned = styleTagPattern.ReplaceAllString(cleaned, "\n")
	cleaned = htmlCommentPattern.ReplaceAllString(cleaned, "\n")
	cleaned = htmlTagPattern.ReplaceAllString(cleaned, "\n")
	cleaned = html.UnescapeString(cleaned)

	lines := strings.Split(cleaned, "\n")
	out := make([]string, 0, len(lines))
	for i := range lines {
		normalized := htmlWhitespacePattern.ReplaceAllString(strings.TrimSpace(lines[i]), " ")
		if normalized == "" {
			continue
		}
		out = append(out, normalized)
	}
	return strings.Join(out, "\n")
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}
