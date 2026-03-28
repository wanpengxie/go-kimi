package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/xiewanpeng/go-kimi/pkg/kimi"
)

const (
	defaultMCPProtocolVersion = "2026-03-26"
	defaultMCPClientName      = "go-kimi"
)

// MCPClient implements MCP protocol handshake, tool discovery, and tool calls.
type MCPClient struct {
	transport Transport

	initMu sync.Mutex

	mu                 sync.RWMutex
	initialized        bool
	protocolVersion    string
	serverCapabilities json.RawMessage
	serverInfo         *ServerInfo
}

// ClientInfo identifies one MCP client in initialize handshake.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerInfo identifies one MCP server returned by initialize handshake.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// MCPToolDefinition describes one tool returned from tools/list.
type MCPToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPToolResult is one tools/call response payload.
type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError"`
}

// MCPContent is one content item returned from tools/call.
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

type initializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      *ServerInfo     `json:"serverInfo"`
}

type listToolsResult struct {
	Tools []MCPToolDefinition `json:"tools"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type notificationTransport interface {
	Notify(ctx context.Context, method string, params any) error
}

// NewMCPClient creates one MCP protocol client backed by transport.
func NewMCPClient(transport Transport) *MCPClient {
	return &MCPClient{transport: transport}
}

// Initialize performs MCP initialize handshake and sends notifications/initialized.
func (c *MCPClient) Initialize(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return errors.New("mcp client: nil client")
	}

	c.initMu.Lock()
	defer c.initMu.Unlock()

	if c.isInitialized() {
		return nil
	}
	if c.transport == nil {
		return errors.New("mcp client: nil transport")
	}

	resultRaw, err := c.transport.Send(ctx, "initialize", initializeParams{
		ProtocolVersion: defaultMCPProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    defaultMCPClientName,
			Version: kimi.Version,
		},
	})
	if err != nil {
		return fmt.Errorf("mcp client: initialize: %w", err)
	}

	var result initializeResult
	if err := decodeMethodResult(resultRaw, &result, "initialize"); err != nil {
		return err
	}
	protocolVersion := strings.TrimSpace(result.ProtocolVersion)
	if protocolVersion == "" {
		return errors.New("mcp client: initialize response missing protocolVersion")
	}

	serverInfo := sanitizeServerInfo(result.ServerInfo)
	if serverInfo == nil {
		return errors.New("mcp client: initialize response missing serverInfo")
	}

	if err := c.sendInitialized(ctx); err != nil {
		return fmt.Errorf("mcp client: notifications/initialized: %w", err)
	}

	c.mu.Lock()
	c.initialized = true
	c.protocolVersion = protocolVersion
	c.serverCapabilities = cloneRawMessage(result.Capabilities)
	c.serverInfo = serverInfo
	c.mu.Unlock()

	return nil
}

// ListTools discovers tool definitions from MCP server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDefinition, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return nil, errors.New("mcp client: nil client")
	}
	if err := c.requireInitialized(); err != nil {
		return nil, err
	}

	resultRaw, err := c.transport.Send(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("mcp client: tools/list: %w", err)
	}

	var result listToolsResult
	if err := decodeMethodResult(resultRaw, &result, "tools/list"); err != nil {
		return nil, err
	}

	tools := make([]MCPToolDefinition, 0, len(result.Tools))
	for i := range result.Tools {
		def := result.Tools[i]
		def.Name = strings.TrimSpace(def.Name)
		if def.Name == "" {
			return nil, fmt.Errorf("mcp client: tools/list returned tool with empty name at index %d", i)
		}
		def.Description = strings.TrimSpace(def.Description)
		def.InputSchema = cloneRawMessage(def.InputSchema)
		tools = append(tools, def)
	}
	return tools, nil
}

// CallTool calls one named tool on MCP server.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*MCPToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return nil, errors.New("mcp client: nil client")
	}
	if err := c.requireInitialized(); err != nil {
		return nil, err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("mcp client: tool name is required")
	}

	resultRaw, err := c.transport.Send(ctx, "tools/call", callToolParams{
		Name:      name,
		Arguments: cloneArguments(args),
	})
	if err != nil {
		return nil, fmt.Errorf("mcp client: tools/call: %w", err)
	}

	var result MCPToolResult
	if err := decodeMethodResult(resultRaw, &result, "tools/call"); err != nil {
		return nil, err
	}
	for i := range result.Content {
		result.Content[i].Type = strings.TrimSpace(result.Content[i].Type)
		result.Content[i].Text = strings.TrimSpace(result.Content[i].Text)
	}

	return &result, nil
}

// Close closes transport and clears cached handshake state.
func (c *MCPClient) Close() error {
	if c == nil {
		return nil
	}

	c.initMu.Lock()
	defer c.initMu.Unlock()

	c.mu.Lock()
	c.initialized = false
	c.protocolVersion = ""
	c.serverCapabilities = nil
	c.serverInfo = nil
	transport := c.transport
	c.mu.Unlock()

	if transport == nil {
		return nil
	}
	return transport.Close()
}

// ServerInfo returns a copy of cached server info after initialization.
func (c *MCPClient) ServerInfo() *ServerInfo {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.serverInfo == nil {
		return nil
	}
	copy := *c.serverInfo
	return &copy
}

func (c *MCPClient) isInitialized() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.initialized
}

func (c *MCPClient) requireInitialized() error {
	if !c.isInitialized() {
		return errors.New("mcp client: initialize is required")
	}
	return nil
}

func (c *MCPClient) sendInitialized(ctx context.Context) error {
	params := map[string]any{}
	if notifier, ok := c.transport.(notificationTransport); ok {
		return notifier.Notify(ctx, "notifications/initialized", params)
	}
	_, err := c.transport.Send(ctx, "notifications/initialized", params)
	return err
}

func decodeMethodResult(raw json.RawMessage, out any, method string) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return fmt.Errorf("mcp client: %s returned empty result", method)
	}
	if err := json.Unmarshal([]byte(trimmed), out); err != nil {
		return fmt.Errorf("mcp client: decode %s result: %w", method, err)
	}
	return nil
}

func sanitizeServerInfo(info *ServerInfo) *ServerInfo {
	if info == nil {
		return nil
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		return nil
	}
	return &ServerInfo{
		Name:    name,
		Version: strings.TrimSpace(info.Version),
	}
}

func cloneArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
