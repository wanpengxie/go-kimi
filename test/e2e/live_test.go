//go:build e2e_live

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
)

func TestLiveProviderConfigInitialization(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Skip("KIMI_API_KEY is not set; skipping live e2e test")
	}

	cfg := config.NewDefaultConfig()
	cfg.Providers = []config.LLMProvider{
		{
			Name:    "moonshot-live",
			Type:    "moonshot",
			APIKey:  apiKey,
			BaseURL: "https://api.moonshot.ai/v1",
		},
	}
	cfg.Models = []config.LLMModel{
		{
			Name:          "kimi-k2",
			Provider:      "moonshot-live",
			ContextWindow: 128000,
		},
	}
	cfg.DefaultProvider = "moonshot-live"
	cfg.DefaultModel = "kimi-k2"

	path := filepath.Join(t.TempDir(), "live-config.toml")
	if err := config.SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("validate loaded config: %v", err)
	}
	if loaded.DefaultProvider != "moonshot-live" {
		t.Fatalf("DefaultProvider = %q, want %q", loaded.DefaultProvider, "moonshot-live")
	}
	if loaded.DefaultModel != "kimi-k2" {
		t.Fatalf("DefaultModel = %q, want %q", loaded.DefaultModel, "kimi-k2")
	}
	if len(loaded.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(loaded.Providers))
	}
	if loaded.Providers[0].Type != "moonshot" {
		t.Fatalf("provider type = %q, want %q", loaded.Providers[0].Type, "moonshot")
	}
	if loaded.Providers[0].APIKey == "" {
		t.Fatal("provider API key should not be empty")
	}
	if loaded.Providers[0].APIKey != apiKey {
		t.Fatal("provider API key changed after save/load")
	}
}
