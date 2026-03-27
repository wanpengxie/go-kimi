//go:build e2e_live

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm/moonshot"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestLiveSoulSingleTurn(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Fatal("KIMI_API_KEY must be set for e2e_live tests")
	}

	baseURL := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("KIMI_MODEL"))
	if model == "" {
		model = "kimi-k2"
	}

	provider := moonshot.NewMoonshotClient(apiKey, baseURL, model)
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: "hello, reply in one sentence"},
	})
	if err != nil {
		t.Fatalf("Run() with live Moonshot provider error = %v", err)
	}

	output := strings.TrimSpace(liveTextFromContentParts(result.Content))
	if output == "" {
		t.Fatalf("live response is empty, result=%#v", result.Content)
	}

	messages := ctxStore.Messages()
	if len(messages) < 2 {
		t.Fatalf("context message count = %d, want >= 2", len(messages))
	}
	if messages[0].Role != soul.RoleUser {
		t.Fatalf("context first role = %q, want user", messages[0].Role)
	}
	if messages[len(messages)-1].Role != soul.RoleAssistant {
		t.Fatalf("context last role = %q, want assistant", messages[len(messages)-1].Role)
	}
}

func liveTextFromContentParts(parts types.ContentParts) string {
	var sb strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			sb.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				sb.WriteString(typed.Text)
			}
		}
	}
	return sb.String()
}
