//go:build e2e_live

package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
)

func TestLiveSmoke(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Skip("KIMI_API_KEY is not set; skipping live e2e test")
	}

	cfg := config.NewDefaultConfig()
	if len(cfg.Providers) == 0 {
		t.Fatal("default config has no providers")
	}
	cfg.Providers[0].APIKey = apiKey

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
}
