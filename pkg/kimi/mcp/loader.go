package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
)

type transportFactory func(cfg MCPServerConfig) (Transport, error)

// MCPToolLoader creates clients from configs and discovers tools.
type MCPToolLoader struct {
	configs []MCPServerConfig

	mu      sync.Mutex
	clients []*MCPClient

	transportFactory transportFactory
}

// NewMCPToolLoader creates one loader from MCP server configs.
func NewMCPToolLoader(configs []MCPServerConfig) *MCPToolLoader {
	copied := make([]MCPServerConfig, len(configs))
	copy(copied, configs)
	return &MCPToolLoader{
		configs:          copied,
		transportFactory: defaultTransportFactory,
	}
}

// LoadAll connects all servers, initializes clients, and returns discovered tools.
func (l *MCPToolLoader) LoadAll(ctx context.Context) ([]tools.Tool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil {
		return nil, errors.New("mcp loader: nil loader")
	}

	configs, err := l.normalizedConfigs()
	if err != nil {
		return nil, err
	}

	if err := l.Close(); err != nil {
		return nil, fmt.Errorf("mcp loader: close existing clients: %w", err)
	}

	factory := l.transportFactory
	if factory == nil {
		factory = defaultTransportFactory
	}

	clients := make([]*MCPClient, 0, len(configs))
	discovered := make([]tools.Tool, 0)

	for i := range configs {
		cfg := configs[i]

		transport, err := factory(cfg)
		if err != nil {
			_ = closeClients(clients)
			return nil, fmt.Errorf("mcp loader: connect server %q: %w", cfg.Name, err)
		}

		client := NewMCPClient(transport)
		if err := client.Initialize(ctx); err != nil {
			_ = client.Close()
			_ = closeClients(clients)
			return nil, fmt.Errorf("mcp loader: initialize server %q: %w", cfg.Name, err)
		}

		definitions, err := client.ListTools(ctx)
		if err != nil {
			_ = client.Close()
			_ = closeClients(clients)
			return nil, fmt.Errorf("mcp loader: list tools from server %q: %w", cfg.Name, err)
		}

		for j := range definitions {
			timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
			discovered = append(discovered, NewMCPToolWithTimeout(client, definitions[j], cfg.Name, timeout))
		}
		clients = append(clients, client)
	}

	l.mu.Lock()
	l.clients = clients
	l.mu.Unlock()
	return discovered, nil
}

// Close closes all active clients managed by the loader.
func (l *MCPToolLoader) Close() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	clients := l.clients
	l.clients = nil
	l.mu.Unlock()

	return closeClients(clients)
}

func (l *MCPToolLoader) normalizedConfigs() ([]MCPServerConfig, error) {
	if l == nil {
		return nil, errors.New("mcp loader: nil loader")
	}

	l.mu.Lock()
	configs := make([]MCPServerConfig, len(l.configs))
	copy(configs, l.configs)
	l.mu.Unlock()

	seen := make(map[string]struct{}, len(configs))
	normalized := make([]MCPServerConfig, 0, len(configs))
	for i := range configs {
		cfg, err := normalizeServerConfig(configs[i])
		if err != nil {
			return nil, fmt.Errorf("mcp loader: config[%d]: %w", i, err)
		}
		nameKey := strings.ToLower(cfg.Name)
		if _, ok := seen[nameKey]; ok {
			return nil, fmt.Errorf("mcp loader: duplicate server name %q", cfg.Name)
		}
		seen[nameKey] = struct{}{}
		normalized = append(normalized, cfg)
	}

	return normalized, nil
}

func defaultTransportFactory(cfg MCPServerConfig) (Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		return NewStdioTransport(cfg.Command, cfg.Args, cfg.Env)
	case TransportSSE:
		return NewSSETransport(cfg.URL, cfg.Headers)
	case TransportStreamableHTTP:
		return NewSSETransport(cfg.URL, cfg.Headers)
	default:
		return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

func closeClients(clients []*MCPClient) error {
	var errs []error
	for i := range clients {
		if clients[i] == nil {
			continue
		}
		if err := clients[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
