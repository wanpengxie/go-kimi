//go:build e2e_live

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm/moonshot"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const liveDefaultBaseURL = "https://api.moonshot.ai/v1"

var livePreferredModels = []string{
	"kimi-latest",
	"moonshot-v1-8k",
	"moonshot-v1-32k",
	"moonshot-v1-128k",
	"kimi-k2",
}

type liveModelResolution struct {
	Model  string
	Source string
}

func TestLiveSoulSingleTurn(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		t.Fatal("KIMI_API_KEY must be set for e2e_live tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	baseURL := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	resolvedModel, err := resolveLiveModel(ctx, apiKey, baseURL, os.Getenv("KIMI_MODEL"))
	if err != nil {
		t.Fatalf("resolve live model error = %v", err)
	}
	t.Logf("live model resolved from %s: %s", resolvedModel.Source, resolvedModel.Model)

	provider := moonshot.NewMoonshotClient(apiKey, baseURL, resolvedModel.Model)
	ctxStore := soul.NewSoulContext(t.TempDir())
	engine := soul.NewSoul(provider, ctxStore, nil, wire.NoopEmitter{}, "")

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

func resolveLiveModel(ctx context.Context, apiKey, baseURL, configuredModel string) (liveModelResolution, error) {
	model := strings.TrimSpace(configuredModel)
	if model != "" {
		return liveModelResolution{
			Model:  model,
			Source: "env:KIMI_MODEL",
		}, nil
	}

	models, err := fetchMoonshotModels(ctx, apiKey, baseURL)
	if err == nil {
		model = chooseLiveModel(models)
		if model == "" {
			return liveModelResolution{}, errors.New("models API returned no usable model id")
		}
		return liveModelResolution{
			Model:  model,
			Source: "api:/models",
		}, nil
	}

	var statusErr *liveHTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
		return liveModelResolution{}, fmt.Errorf("models API unauthorized, check KIMI_API_KEY: %w", err)
	}

	return liveModelResolution{
		Model:  "moonshot-v1-8k",
		Source: "fallback:moonshot-v1-8k",
	}, nil
}

func fetchMoonshotModels(ctx context.Context, apiKey, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveModelsEndpoint(baseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request models API: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &liveHTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(payload)),
		}
	}

	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode models API response: %w", err)
	}

	models := make([]string, 0, len(decoded.Data))
	for i := range decoded.Data {
		id := strings.TrimSpace(decoded.Data[i].ID)
		if id == "" {
			continue
		}
		models = append(models, id)
	}

	return models, nil
}

func liveModelsEndpoint(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalized == "" {
		normalized = liveDefaultBaseURL
	}
	normalized = strings.TrimSuffix(normalized, "/chat/completions")
	return normalized + "/models"
}

func chooseLiveModel(models []string) string {
	if len(models) == 0 {
		return ""
	}

	unique := make(map[string]string, len(models))
	for i := range models {
		model := strings.TrimSpace(models[i])
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, exists := unique[key]; !exists {
			unique[key] = model
		}
	}
	if len(unique) == 0 {
		return ""
	}

	for i := range livePreferredModels {
		preferred := strings.ToLower(strings.TrimSpace(livePreferredModels[i]))
		if preferred == "" {
			continue
		}
		if model, ok := unique[preferred]; ok {
			return model
		}
	}

	chatCandidates := make([]string, 0, len(unique))
	for _, model := range unique {
		if isLikelyChatModel(model) {
			chatCandidates = append(chatCandidates, model)
		}
	}
	sort.Strings(chatCandidates)
	if len(chatCandidates) > 0 {
		return chatCandidates[0]
	}

	allModels := make([]string, 0, len(unique))
	for _, model := range unique {
		allModels = append(allModels, model)
	}
	sort.Strings(allModels)
	return allModels[0]
}

func isLikelyChatModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	excludedKeywords := []string{"embedding", "rerank", "moderation", "whisper", "tts", "speech"}
	for i := range excludedKeywords {
		if strings.Contains(normalized, excludedKeywords[i]) {
			return false
		}
	}
	return true
}

type liveHTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *liveHTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("status %d", e.StatusCode)
	}
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}
