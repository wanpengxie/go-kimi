// Package config provides strongly-typed SDK configuration models.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	defaultMaxTurns            = 20
	defaultMaxToolCallsPerTurn = 8
	defaultMaxRetriesPerStep   = 3

	defaultBackgroundMaxParallelTasks = 4
	defaultBackgroundTaskTimeoutSec   = 300

	defaultServiceTimeoutSec   = 30
	defaultSearchMaxResults    = 8
	defaultFetchMaxContentByte = 2 * 1024 * 1024

	defaultDefaultThinking = "off"
	defaultDefaultYolo     = true
)

// OAuthRef references an OAuth account.
type OAuthRef struct {
	Provider  string `toml:"provider" json:"provider"`
	AccountID string `toml:"account_id" json:"account_id"`
}

// SecretStr stores sensitive string values while redacting String() output.
type SecretStr string

// Raw returns the plain underlying value.
func (s SecretStr) Raw() string {
	return string(s)
}

// String redacts sensitive values for fmt/log output.
func (s SecretStr) String() string {
	if strings.TrimSpace(string(s)) == "" {
		return ""
	}
	return "[REDACTED]"
}

// LLMProvider defines a provider endpoint and credentials.
type LLMProvider struct {
	Name    string    `toml:"name" json:"name"`
	Type    string    `toml:"type" json:"type"`
	APIKey  SecretStr `toml:"api_key,omitempty" json:"api_key,omitempty"`
	BaseURL string    `toml:"base_url,omitempty" json:"base_url,omitempty"`
	OAuth   *OAuthRef `toml:"oauth,omitempty" json:"oauth,omitempty"`
}

// LLMModel defines an available model under a provider.
type LLMModel struct {
	Name            string                  `toml:"name" json:"name"`
	Provider        string                  `toml:"provider" json:"provider"`
	ContextWindow   int                     `toml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutputTokens int                     `toml:"max_output_tokens,omitempty" json:"max_output_tokens,omitempty"`
	Capabilities    []types.ModelCapability `toml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// LoopControl defines turn-loop guard rails.
type LoopControl struct {
	MaxTurns            int `toml:"max_turns" json:"max_turns"`
	MaxToolCallsPerTurn int `toml:"max_tool_calls_per_turn" json:"max_tool_calls_per_turn"`
	MaxRetriesPerStep   int `toml:"max_retries_per_step" json:"max_retries_per_step"`
}

// Validate validates loop control constraints.
func (c LoopControl) Validate() error {
	if c.MaxTurns < 1 {
		return errors.New("loop.max_turns must be >= 1")
	}
	if c.MaxToolCallsPerTurn < 1 {
		return errors.New("loop.max_tool_calls_per_turn must be >= 1")
	}
	if c.MaxRetriesPerStep < 0 {
		return errors.New("loop.max_retries_per_step must be >= 0")
	}
	return nil
}

// BackgroundConfig controls background execution behavior.
type BackgroundConfig struct {
	MaxParallelTasks  int `toml:"max_parallel_tasks" json:"max_parallel_tasks"`
	TaskTimeoutSecond int `toml:"task_timeout_seconds" json:"task_timeout_seconds"`
}

// Validate validates background config constraints.
func (c BackgroundConfig) Validate() error {
	if c.MaxParallelTasks < 1 {
		return errors.New("background.max_parallel_tasks must be >= 1")
	}
	if c.TaskTimeoutSecond < 1 {
		return errors.New("background.task_timeout_seconds must be >= 1")
	}
	return nil
}

// NotificationConfig controls notification behavior.
type NotificationConfig struct {
	Enabled   bool `toml:"enabled" json:"enabled"`
	OnTurnEnd bool `toml:"on_turn_end" json:"on_turn_end"`
	OnError   bool `toml:"on_error" json:"on_error"`
}

// MoonshotSearchConfig configures search service behavior.
type MoonshotSearchConfig struct {
	Enabled        bool   `toml:"enabled" json:"enabled"`
	Endpoint       string `toml:"endpoint" json:"endpoint"`
	TimeoutSeconds int    `toml:"timeout_seconds" json:"timeout_seconds"`
	MaxResults     int    `toml:"max_results" json:"max_results"`
}

// MoonshotFetchConfig configures fetch service behavior.
type MoonshotFetchConfig struct {
	Enabled         bool   `toml:"enabled" json:"enabled"`
	Endpoint        string `toml:"endpoint" json:"endpoint"`
	TimeoutSeconds  int    `toml:"timeout_seconds" json:"timeout_seconds"`
	MaxContentBytes int64  `toml:"max_content_bytes" json:"max_content_bytes"`
}

// Services groups auxiliary service configuration.
type Services struct {
	MoonshotSearch MoonshotSearchConfig `toml:"moonshot_search" json:"moonshot_search"`
	MoonshotFetch  MoonshotFetchConfig  `toml:"moonshot_fetch" json:"moonshot_fetch"`
}

// Validate validates service constraints.
func (s Services) Validate() error {
	if s.MoonshotSearch.Enabled {
		if strings.TrimSpace(s.MoonshotSearch.Endpoint) == "" {
			return errors.New("services.moonshot_search.endpoint must not be empty when enabled=true")
		}
		if s.MoonshotSearch.TimeoutSeconds < 1 {
			return errors.New("services.moonshot_search.timeout_seconds must be >= 1 when enabled=true")
		}
		if s.MoonshotSearch.MaxResults < 1 {
			return errors.New("services.moonshot_search.max_results must be >= 1 when enabled=true")
		}
	}

	if s.MoonshotFetch.Enabled {
		if strings.TrimSpace(s.MoonshotFetch.Endpoint) == "" {
			return errors.New("services.moonshot_fetch.endpoint must not be empty when enabled=true")
		}
		if s.MoonshotFetch.TimeoutSeconds < 1 {
			return errors.New("services.moonshot_fetch.timeout_seconds must be >= 1 when enabled=true")
		}
		if s.MoonshotFetch.MaxContentBytes < 1 {
			return errors.New("services.moonshot_fetch.max_content_bytes must be >= 1 when enabled=true")
		}
	}
	return nil
}

// MCPClientConfig defines one MCP client declaration.
type MCPClientConfig struct {
	Name           string            `toml:"name" json:"name"`
	Command        string            `toml:"command" json:"command"`
	Args           []string          `toml:"args,omitempty" json:"args,omitempty"`
	Env            map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	TimeoutSeconds int               `toml:"timeout_seconds" json:"timeout_seconds"`
	Disabled       bool              `toml:"disabled,omitempty" json:"disabled,omitempty"`
}

// MCPConfig groups MCP clients.
type MCPConfig struct {
	Clients []MCPClientConfig `toml:"clients" json:"clients"`
}

// Validate validates MCP config constraints.
func (c MCPConfig) Validate() error {
	seen := make(map[string]struct{}, len(c.Clients))
	for i, client := range c.Clients {
		name := strings.TrimSpace(client.Name)
		if name == "" {
			return fmt.Errorf("mcp.clients[%d].name must not be empty", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("mcp.clients[%d].name duplicates %q", i, name)
		}
		seen[name] = struct{}{}

		if !client.Disabled && strings.TrimSpace(client.Command) == "" {
			return fmt.Errorf("mcp.clients[%d].command must not be empty when disabled=false", i)
		}
		if !client.Disabled && client.TimeoutSeconds < 1 {
			return fmt.Errorf("mcp.clients[%d].timeout_seconds must be >= 1 when disabled=false", i)
		}
		for key := range client.Env {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("mcp.clients[%d].env contains empty key", i)
			}
		}
	}
	return nil
}

// Config is the top-level SDK configuration.
type Config struct {
	OAuth           OAuthRef           `toml:"oauth" json:"oauth"`
	Providers       []LLMProvider      `toml:"providers" json:"providers"`
	Models          []LLMModel         `toml:"models" json:"models"`
	DefaultProvider string             `toml:"default_provider,omitempty" json:"default_provider,omitempty"`
	DefaultModel    string             `toml:"default_model,omitempty" json:"default_model,omitempty"`
	DefaultThinking string             `toml:"default_thinking,omitempty" json:"default_thinking,omitempty"`
	DefaultYolo     bool               `toml:"default_yolo" json:"default_yolo"`
	Loop            LoopControl        `toml:"loop" json:"loop"`
	Background      BackgroundConfig   `toml:"background" json:"background"`
	Notification    NotificationConfig `toml:"notification" json:"notification"`
	Services        Services           `toml:"services" json:"services"`
	MCP             MCPConfig          `toml:"mcp" json:"mcp"`
}

// NewDefaultLoopControl returns loop defaults.
func NewDefaultLoopControl() LoopControl {
	return LoopControl{
		MaxTurns:            defaultMaxTurns,
		MaxToolCallsPerTurn: defaultMaxToolCallsPerTurn,
		MaxRetriesPerStep:   defaultMaxRetriesPerStep,
	}
}

// NewDefaultBackgroundConfig returns background defaults.
func NewDefaultBackgroundConfig() BackgroundConfig {
	return BackgroundConfig{
		MaxParallelTasks:  defaultBackgroundMaxParallelTasks,
		TaskTimeoutSecond: defaultBackgroundTaskTimeoutSec,
	}
}

// NewDefaultNotificationConfig returns notification defaults.
func NewDefaultNotificationConfig() NotificationConfig {
	return NotificationConfig{
		Enabled:   true,
		OnTurnEnd: true,
		OnError:   true,
	}
}

// NewDefaultServices returns auxiliary service defaults.
func NewDefaultServices() Services {
	return Services{
		MoonshotSearch: MoonshotSearchConfig{
			Enabled:        true,
			Endpoint:       "https://api.moonshot.ai/v1/search",
			TimeoutSeconds: defaultServiceTimeoutSec,
			MaxResults:     defaultSearchMaxResults,
		},
		MoonshotFetch: MoonshotFetchConfig{
			Enabled:         true,
			Endpoint:        "https://api.moonshot.ai/v1/fetch",
			TimeoutSeconds:  defaultServiceTimeoutSec,
			MaxContentBytes: defaultFetchMaxContentByte,
		},
	}
}

// NewDefaultMCPConfig returns MCP defaults.
func NewDefaultMCPConfig() MCPConfig {
	return MCPConfig{
		Clients: []MCPClientConfig{},
	}
}

// NewDefaultConfig returns a valid baseline configuration.
func NewDefaultConfig() Config {
	return Config{
		Providers: []LLMProvider{
			{
				Name:    "moonshot",
				Type:    "moonshot",
				BaseURL: "https://api.moonshot.ai/v1",
			},
		},
		Models: []LLMModel{
			{
				Name:          "kimi-k2",
				Provider:      "moonshot",
				ContextWindow: 128000,
				Capabilities: []types.ModelCapability{
					types.ModelCapabilityReasoning,
					types.ModelCapabilityToolCall,
				},
			},
		},
		DefaultProvider: "moonshot",
		DefaultModel:    "kimi-k2",
		DefaultThinking: defaultDefaultThinking,
		DefaultYolo:     defaultDefaultYolo,
		Loop:            NewDefaultLoopControl(),
		Background:      NewDefaultBackgroundConfig(),
		Notification:    NewDefaultNotificationConfig(),
		Services:        NewDefaultServices(),
		MCP:             NewDefaultMCPConfig(),
	}
}

// LoadConfig loads and validates TOML config from path.
func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("load config: path is empty")
	}

	config := NewDefaultConfig()
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return config, nil
}

// LoadConfigWithEnv loads config from path and then applies environment overrides.
func LoadConfigWithEnv(path string) (Config, error) {
	config, err := LoadConfig(path)
	if err != nil {
		return Config{}, err
	}
	ApplyEnvOverrides(&config)
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("load config with env %q: %w", path, err)
	}
	return config, nil
}

// ApplyEnvOverrides applies environment-variable overrides in-place.
func ApplyEnvOverrides(config *Config) {
	applyEnvOverrides(config, os.LookupEnv)
}

// SaveConfig validates and writes config as TOML.
func SaveConfig(config Config, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("save config: path is empty")
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("save config %q: mkdir %q: %w", path, dir, err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("save config %q: %w", path, err)
	}
	if err := toml.NewEncoder(file).Encode(config); err != nil {
		_ = file.Close()
		return fmt.Errorf("save config %q: encode toml: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("save config %q: close file: %w", path, err)
	}
	return nil
}

// Validate validates the top-level config and nested structures.
func (c Config) Validate() error {
	if err := c.Loop.Validate(); err != nil {
		return err
	}
	if err := c.Background.Validate(); err != nil {
		return err
	}
	if err := c.Services.Validate(); err != nil {
		return err
	}
	if err := c.MCP.Validate(); err != nil {
		return err
	}

	providerByName := make(map[string]LLMProvider, len(c.Providers))
	for i, provider := range c.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			return fmt.Errorf("providers[%d].name must not be empty", i)
		}
		if strings.TrimSpace(provider.Type) == "" {
			return fmt.Errorf("providers[%d].type must not be empty", i)
		}
		if _, ok := providerByName[name]; ok {
			return fmt.Errorf("providers[%d].name duplicates %q", i, name)
		}
		if provider.OAuth != nil {
			if strings.TrimSpace(provider.OAuth.Provider) == "" {
				return fmt.Errorf("providers[%d].oauth.provider must not be empty", i)
			}
			if strings.TrimSpace(provider.OAuth.AccountID) == "" {
				return fmt.Errorf("providers[%d].oauth.account_id must not be empty", i)
			}
		}
		providerByName[name] = provider
	}

	modelProviderByName := make(map[string]string, len(c.Models))
	for i, model := range c.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			return fmt.Errorf("models[%d].name must not be empty", i)
		}
		if _, ok := modelProviderByName[name]; ok {
			return fmt.Errorf("models[%d].name duplicates %q", i, name)
		}
		providerName := strings.TrimSpace(model.Provider)
		if providerName == "" {
			return fmt.Errorf("models[%d].provider must not be empty", i)
		}
		if _, ok := providerByName[providerName]; !ok {
			return fmt.Errorf("models[%d].provider %q does not exist", i, providerName)
		}
		if model.ContextWindow < 0 {
			return fmt.Errorf("models[%d].context_window must be >= 0", i)
		}
		if model.MaxOutputTokens < 0 {
			return fmt.Errorf("models[%d].max_output_tokens must be >= 0", i)
		}
		for j, capability := range model.Capabilities {
			if strings.TrimSpace(string(capability)) == "" {
				return fmt.Errorf("models[%d].capabilities[%d] must not be empty", i, j)
			}
		}
		modelProviderByName[name] = providerName
	}

	if len(c.Providers) > 0 {
		if strings.TrimSpace(c.DefaultProvider) == "" {
			return errors.New("default_provider must not be empty when providers are configured")
		}
		if _, ok := providerByName[c.DefaultProvider]; !ok {
			return fmt.Errorf("default_provider %q does not exist", c.DefaultProvider)
		}
	}
	if len(c.Models) > 0 {
		if strings.TrimSpace(c.DefaultModel) == "" {
			return errors.New("default_model must not be empty when models are configured")
		}
		modelProvider, ok := modelProviderByName[c.DefaultModel]
		if !ok {
			return fmt.Errorf("default_model %q does not exist", c.DefaultModel)
		}
		if c.DefaultProvider != "" && modelProvider != c.DefaultProvider {
			return fmt.Errorf("default_model %q belongs to provider %q, but default_provider is %q", c.DefaultModel, modelProvider, c.DefaultProvider)
		}
	}

	switch strings.ToLower(strings.TrimSpace(c.DefaultThinking)) {
	case "", "off", "low", "medium", "high":
	default:
		return fmt.Errorf("default_thinking %q is invalid", c.DefaultThinking)
	}

	return nil
}

type lookupEnvFunc func(key string) (string, bool)

func applyEnvOverrides(config *Config, lookup lookupEnvFunc) {
	if config == nil || lookup == nil {
		return
	}

	for i := range config.Providers {
		envKey := providerAPIKeyEnvKey(config.Providers[i].Type)
		if envKey == "" {
			continue
		}
		value, ok := lookup(envKey)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		config.Providers[i].APIKey = SecretStr(value)
	}
}

func providerAPIKeyEnvKey(providerType string) string {
	switch normalizeProviderType(providerType) {
	case "moonshot", "kimi":
		return "KIMI_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "gemini", "google":
		return "GEMINI_API_KEY"
	case "azure_openai":
		return "AZURE_OPENAI_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	default:
		return ""
	}
}

func normalizeProviderType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
