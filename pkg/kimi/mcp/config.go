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
)

// MCPServerConfig describes one MCP server connection config.
type MCPServerConfig struct {
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`

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
		Name:      strings.TrimSpace(cfg.Name),
		Transport: normalizeTransportType(cfg.Transport),
		Command:   strings.TrimSpace(cfg.Command),
		Args:      cloneStrings(cfg.Args),
		Env:       cloneStringMap(cfg.Env),
		URL:       strings.TrimSpace(cfg.URL),
		Headers:   cloneStringMap(cfg.Headers),
	}

	if out.Name == "" {
		return MCPServerConfig{}, fmt.Errorf("mcp config: server name is required")
	}

	switch out.Transport {
	case TransportStdio:
		if out.Command == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: command is required for stdio transport", out.Name)
		}
	case TransportSSE:
		if out.URL == "" {
			return MCPServerConfig{}, fmt.Errorf("mcp config %q: url is required for sse transport", out.Name)
		}
	default:
		return MCPServerConfig{}, fmt.Errorf("mcp config %q: unsupported transport %q", out.Name, out.Transport)
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
	return TransportType(strings.ToLower(strings.TrimSpace(string(t))))
}
