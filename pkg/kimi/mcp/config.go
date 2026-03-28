package mcp

import (
	"fmt"
	"strings"
)

// TransportType identifies MCP transport mode.
type TransportType string

const (
	// TransportStdio uses stdio child process transport.
	TransportStdio TransportType = "stdio"
	// TransportSSE uses HTTP+SSE transport.
	TransportSSE TransportType = "sse"
	// TransportStreamableHTTP uses MCP streamable HTTP transport.
	TransportStreamableHTTP TransportType = "streamable_http"
)

// MCPServerConfig describes one MCP server connection config.
type MCPServerConfig struct {
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`
	// TimeoutSeconds controls per-tool call timeout for tools exposed by this server.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// Stdio settings.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// SSE settings.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func normalizeServerConfig(cfg MCPServerConfig) (MCPServerConfig, error) {
	out := MCPServerConfig{
		Name:           strings.TrimSpace(cfg.Name),
		Transport:      normalizeTransportType(cfg.Transport),
		TimeoutSeconds: cfg.TimeoutSeconds,
		Command:        strings.TrimSpace(cfg.Command),
		Args:           cloneStrings(cfg.Args),
		Env:            cloneStringMap(cfg.Env),
		URL:            strings.TrimSpace(cfg.URL),
		Headers:        cloneStringMap(cfg.Headers),
	}

	if out.Name == "" {
		return MCPServerConfig{}, fmt.Errorf("mcp config: server name is required")
	}

	switch out.Transport {
	case TransportStdio:
		if out.Command == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: command is required for stdio transport", out.Name)
		}
	case TransportSSE, TransportStreamableHTTP:
		if out.URL == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: url is required for sse transport", out.Name)
		}
	default:
		return MCPServerConfig{}, fmt.Errorf("mcp config %q: unsupported transport %q", out.Name, out.Transport)
	}
	if out.TimeoutSeconds < 0 {
		return MCPServerConfig{}, fmt.Errorf("mcp config %q: timeout_seconds must be >= 0", out.Name)
	}

	for key := range out.Env {
		if strings.TrimSpace(key) == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: env contains empty key", out.Name)
		}
	}
	for key := range out.Headers {
		if strings.TrimSpace(key) == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: headers contains empty key", out.Name)
		}
	}

	return out, nil
}

func normalizeTransportType(t TransportType) TransportType {
	normalized := strings.ToLower(strings.TrimSpace(string(t)))
	switch normalized {
	case "streamable-http":
		return TransportStreamableHTTP
	default:
		return TransportType(normalized)
	}
}
