package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestNewDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	if cfg.DefaultProvider != "moonshot" {
		t.Fatalf("DefaultProvider = %q, want %q", cfg.DefaultProvider, "moonshot")
	}
	if cfg.DefaultModel != "kimi-k2" {
		t.Fatalf("DefaultModel = %q, want %q", cfg.DefaultModel, "kimi-k2")
	}
	if cfg.Loop.MaxTurns != defaultMaxTurns {
		t.Fatalf("Loop.MaxTurns = %d, want %d", cfg.Loop.MaxTurns, defaultMaxTurns)
	}
	if cfg.Background.TaskTimeoutSecond != defaultBackgroundTaskTimeoutSec {
		t.Fatalf("Background.TaskTimeoutSecond = %d, want %d", cfg.Background.TaskTimeoutSecond, defaultBackgroundTaskTimeoutSec)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid, got error: %v", err)
	}
}

func TestSaveLoadConfigRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	cfg.Providers[0].APIKey = "secret"
	cfg.Providers[0].OAuth = &OAuthRef{
		Provider:  "moonshot",
		AccountID: "acct-001",
	}
	cfg.MCP.Clients = []MCPClientConfig{
		{
			Name:           "filesystem",
			Command:        "npx",
			Args:           []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			Env:            map[string]string{"NODE_NO_WARNINGS": "1"},
			TimeoutSeconds: 90,
		},
	}
	cfg.Models[0].MaxOutputTokens = 8192
	cfg.Models[0].Capabilities = append(cfg.Models[0].Capabilities, types.ModelCapabilityVision)

	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("loaded config differs from saved config\nloaded=%#v\nsaved=%#v", loaded, cfg)
	}
}

func TestLoadConfigKeepsDefaultsForOmittedSections(t *testing.T) {
	t.Parallel()

	const minimal = `
default_provider = "moonshot"
default_model = "kimi-k2"

[[providers]]
name = "moonshot"
type = "moonshot"

[[models]]
name = "kimi-k2"
provider = "moonshot"
`

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Loop.MaxTurns != defaultMaxTurns {
		t.Fatalf("Loop.MaxTurns = %d, want %d", cfg.Loop.MaxTurns, defaultMaxTurns)
	}
	if cfg.Background.MaxParallelTasks != defaultBackgroundMaxParallelTasks {
		t.Fatalf("Background.MaxParallelTasks = %d, want %d", cfg.Background.MaxParallelTasks, defaultBackgroundMaxParallelTasks)
	}
	if cfg.Services.MoonshotSearch.Endpoint == "" || cfg.Services.MoonshotFetch.Endpoint == "" {
		t.Fatalf("services endpoints should keep defaults, got %#v", cfg.Services)
	}
}

func TestConfigValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(Config) Config
		wantSubstr string
	}{
		{
			name: "invalid loop max turns",
			mutate: func(c Config) Config {
				c.Loop.MaxTurns = 0
				return c
			},
			wantSubstr: "loop.max_turns",
		},
		{
			name: "invalid background timeout",
			mutate: func(c Config) Config {
				c.Background.TaskTimeoutSecond = 0
				return c
			},
			wantSubstr: "background.task_timeout_seconds",
		},
		{
			name: "duplicate provider names",
			mutate: func(c Config) Config {
				c.Providers = append(c.Providers, c.Providers[0])
				return c
			},
			wantSubstr: "duplicates",
		},
		{
			name: "model references missing provider",
			mutate: func(c Config) Config {
				c.Models[0].Provider = "missing"
				return c
			},
			wantSubstr: "does not exist",
		},
		{
			name: "default model-provider mismatch",
			mutate: func(c Config) Config {
				c.Providers = append(c.Providers, LLMProvider{
					Name: "other",
					Type: "other",
				})
				c.DefaultProvider = "other"
				return c
			},
			wantSubstr: "default_model",
		},
		{
			name: "empty capability value",
			mutate: func(c Config) Config {
				c.Models[0].Capabilities = append(c.Models[0].Capabilities, "")
				return c
			},
			wantSubstr: "capabilities",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.mutate(NewDefaultConfig())
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestLoadConfigErrors(t *testing.T) {
	t.Parallel()

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("LoadConfig(\"\") expected error")
	}

	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("LoadConfig(missing) expected error")
	}

	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("default_provider = [\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig(invalid toml) expected error")
	}
}

func TestSaveConfigErrors(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	if err := SaveConfig(cfg, ""); err == nil {
		t.Fatal("SaveConfig with empty path expected error")
	}

	invalid := cfg
	invalid.Loop.MaxTurns = 0
	if err := SaveConfig(invalid, filepath.Join(t.TempDir(), "config.toml")); err == nil {
		t.Fatal("SaveConfig with invalid config expected error")
	}

	if err := SaveConfig(cfg, t.TempDir()); err == nil {
		t.Fatal("SaveConfig(path is directory) expected error")
	}
}
