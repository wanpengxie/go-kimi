package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
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
	if cfg.Loop.MaxRetriesPerStep != defaultMaxRetriesPerStep {
		t.Fatalf("Loop.MaxRetriesPerStep = %d, want %d", cfg.Loop.MaxRetriesPerStep, defaultMaxRetriesPerStep)
	}
	if cfg.DefaultThinking != defaultDefaultThinking {
		t.Fatalf("DefaultThinking = %q, want %q", cfg.DefaultThinking, defaultDefaultThinking)
	}
	if cfg.DefaultYolo != defaultDefaultYolo {
		t.Fatalf("DefaultYolo = %v, want %v", cfg.DefaultYolo, defaultDefaultYolo)
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
			name: "invalid loop max retries per step",
			mutate: func(c Config) Config {
				c.Loop.MaxRetriesPerStep = -1
				return c
			},
			wantSubstr: "loop.max_retries_per_step",
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
			name: "default provider does not exist",
			mutate: func(c Config) Config {
				c.DefaultProvider = "missing-provider"
				return c
			},
			wantSubstr: "default_provider",
		},
		{
			name: "default model does not exist",
			mutate: func(c Config) Config {
				c.DefaultModel = "missing-model"
				return c
			},
			wantSubstr: "default_model",
		},
		{
			name: "provider name missing",
			mutate: func(c Config) Config {
				c.Providers[0].Name = "  "
				return c
			},
			wantSubstr: "providers[0].name",
		},
		{
			name: "model name missing",
			mutate: func(c Config) Config {
				c.Models[0].Name = " "
				return c
			},
			wantSubstr: "models[0].name",
		},
		{
			name: "model provider missing",
			mutate: func(c Config) Config {
				c.Models[0].Provider = " "
				return c
			},
			wantSubstr: "models[0].provider",
		},
		{
			name: "empty capability value",
			mutate: func(c Config) Config {
				c.Models[0].Capabilities = append(c.Models[0].Capabilities, "")
				return c
			},
			wantSubstr: "capabilities",
		},
		{
			name: "search enabled with empty endpoint",
			mutate: func(c Config) Config {
				c.Services.MoonshotSearch.Endpoint = "   "
				return c
			},
			wantSubstr: "services.moonshot_search.endpoint",
		},
		{
			name: "fetch enabled with invalid max content bytes",
			mutate: func(c Config) Config {
				c.Services.MoonshotFetch.MaxContentBytes = 0
				return c
			},
			wantSubstr: "services.moonshot_fetch.max_content_bytes",
		},
		{
			name: "mcp duplicate client name",
			mutate: func(c Config) Config {
				c.MCP.Clients = []MCPClientConfig{
					{
						Name:           "filesystem",
						Command:        "cmd-a",
						TimeoutSeconds: 10,
					},
					{
						Name:           "filesystem",
						Command:        "cmd-b",
						TimeoutSeconds: 10,
					},
				}
				return c
			},
			wantSubstr: "duplicates",
		},
		{
			name: "mcp enabled client missing command",
			mutate: func(c Config) Config {
				c.MCP.Clients = []MCPClientConfig{
					{
						Name:           "filesystem",
						TimeoutSeconds: 10,
					},
				}
				return c
			},
			wantSubstr: "command must not be empty",
		},
		{
			name: "mcp env contains empty key",
			mutate: func(c Config) Config {
				c.MCP.Clients = []MCPClientConfig{
					{
						Name:           "filesystem",
						Command:        "cmd",
						TimeoutSeconds: 10,
						Env: map[string]string{
							"": "bad",
						},
					},
				}
				return c
			},
			wantSubstr: "env contains empty key",
		},
		{
			name: "provider oauth account id empty",
			mutate: func(c Config) Config {
				c.Providers[0].OAuth = &OAuthRef{
					Provider:  "moonshot",
					AccountID: " ",
				}
				return c
			},
			wantSubstr: "oauth.account_id",
		},
		{
			name: "invalid default thinking",
			mutate: func(c Config) Config {
				c.DefaultThinking = "ultra"
				return c
			},
			wantSubstr: "default_thinking",
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

func TestLoadConfigWithEnvOverridesProviderAPIKeys(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Providers = append(cfg.Providers, LLMProvider{
		Name:    "openai",
		Type:    "openai",
		BaseURL: "https://api.openai.com/v1",
	})
	cfg.Models = append(cfg.Models, LLMModel{
		Name:     "gpt-4.1",
		Provider: "openai",
	})

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	t.Setenv("KIMI_API_KEY", "env-kimi-key")
	t.Setenv("OPENAI_API_KEY", "env-openai-key")

	loaded, err := LoadConfigWithEnv(path)
	if err != nil {
		t.Fatalf("LoadConfigWithEnv() error = %v", err)
	}

	byName := make(map[string]LLMProvider, len(loaded.Providers))
	for i := range loaded.Providers {
		byName[loaded.Providers[i].Name] = loaded.Providers[i]
	}
	if got := byName["moonshot"].APIKey.Raw(); got != "env-kimi-key" {
		t.Fatalf("moonshot APIKey = %q, want env-kimi-key", got)
	}
	if got := byName["openai"].APIKey.Raw(); got != "env-openai-key" {
		t.Fatalf("openai APIKey = %q, want env-openai-key", got)
	}
}

func TestSecretStrStringRedacts(t *testing.T) {
	t.Parallel()

	secret := SecretStr("top-secret")
	if got := secret.String(); got != "[REDACTED]" {
		t.Fatalf("SecretStr.String() = %q, want [REDACTED]", got)
	}
	if got := fmt.Sprintf("%s", secret); got != "[REDACTED]" {
		t.Fatalf("fmt.Sprintf(\"%%s\", SecretStr) = %q, want [REDACTED]", got)
	}
	text, err := secret.MarshalText()
	if err != nil {
		t.Fatalf("SecretStr.MarshalText() error = %v", err)
	}
	if got := string(text); got != "[REDACTED]" {
		t.Fatalf("SecretStr.MarshalText() = %q, want [REDACTED]", got)
	}
	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("json.Marshal(SecretStr) error = %v", err)
	}
	if got := string(encoded); got != `"[REDACTED]"` {
		t.Fatalf("json.Marshal(SecretStr) = %q, want %q", got, `"[REDACTED]"`)
	}
	if got := secret.Raw(); got != "top-secret" {
		t.Fatalf("SecretStr.Raw() = %q, want top-secret", got)
	}
}

func TestConfigJSONMarshalRedactsProviderAPIKey(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	cfg.Providers[0].APIKey = "top-secret"

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(config) error = %v", err)
	}

	payload := string(encoded)
	if strings.Contains(payload, "top-secret") {
		t.Fatalf("json payload leaked raw secret: %s", payload)
	}
	if !strings.Contains(payload, `"api_key":"[REDACTED]"`) {
		t.Fatalf("json payload should contain redacted api_key, got: %s", payload)
	}
}

func TestSaveConfigPersistsRawAPIKey(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	cfg.Providers[0].APIKey = "top-secret"

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	content := string(raw)
	if strings.Contains(content, "[REDACTED]") {
		t.Fatalf("save config wrote redacted value, got: %s", content)
	}
	if !strings.Contains(content, `api_key = "top-secret"`) {
		t.Fatalf("save config did not persist raw api_key, got: %s", content)
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

	blockedPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if err := SaveConfig(cfg, filepath.Join(blockedPath, "config.toml")); err == nil {
		t.Fatal("SaveConfig(path under regular file) expected error")
	}
}

func TestCrossPackageCoverageScenarioFromConfig(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid, got error: %v", err)
	}

	parts := types.ContentParts{
		types.TextPart{Text: "hello"},
		types.ThinkPart{Think: "trace"},
		types.ImageURLPart{ImageURL: "https://example.com/a.png"},
		types.AudioURLPart{AudioURL: "https://example.com/a.mp3"},
		types.VideoURLPart{VideoURL: "https://example.com/a.mp4"},
	}
	partsJSON, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("json.Marshal(ContentParts) error = %v", err)
	}
	var decodedParts types.ContentParts
	if err := json.Unmarshal(partsJSON, &decodedParts); err != nil {
		t.Fatalf("json.Unmarshal(ContentParts) error = %v", err)
	}
	if len(decodedParts) != len(parts) {
		t.Fatalf("decoded parts length = %d, want %d", len(decodedParts), len(parts))
	}

	toolPart := types.ToolCallPart{
		ToolCall: types.ToolCall{
			ID:   "call-1",
			Name: "search",
			Arguments: map[string]any{
				"q": "go coverage",
			},
		},
	}
	toolPartJSON, err := json.Marshal(toolPart)
	if err != nil {
		t.Fatalf("json.Marshal(ToolCallPart) error = %v", err)
	}
	var decodedToolPart types.ToolCallPart
	if err := json.Unmarshal(toolPartJSON, &decodedToolPart); err != nil {
		t.Fatalf("json.Unmarshal(ToolCallPart) error = %v", err)
	}
	if decodedToolPart.ToolCall.ID != "call-1" {
		t.Fatalf("decoded tool call id = %q, want %q", decodedToolPart.ToolCall.ID, "call-1")
	}

	result := types.ToolResult{
		ToolCallID: "call-1",
		Name:       "search",
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"items": []any{"a", "b"},
			},
		},
		IsError: true,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(ToolResult) error = %v", err)
	}
	var decodedResult types.ToolResult
	if err := json.Unmarshal(resultJSON, &decodedResult); err != nil {
		t.Fatalf("json.Unmarshal(ToolResult) error = %v", err)
	}
	if decodedResult.ToolCallID != result.ToolCallID || !decodedResult.IsError {
		t.Fatalf("decoded result = %#v, want tool_call_id=%q and is_error=true", decodedResult, result.ToolCallID)
	}

	blocks := types.DisplayBlocks{
		types.BriefDisplayBlock{Text: "brief"},
		types.DiffDisplayBlock{Title: "patch", Diff: "@@ -1 +1 @@"},
		types.TodoDisplayBlock{
			Title: "todo",
			Items: []types.TodoDisplayItem{
				{Text: "a", Done: false},
				{Text: "b", Done: true},
			},
		},
		types.ShellDisplayBlock{Command: "go test ./...", Output: "ok", ExitCode: 0},
		types.BackgroundTaskDisplayBlock{TaskID: "bg-1", Status: "done", Message: "complete"},
		types.UnknownDisplayBlock{
			OriginalType: "markdown",
			Payload: map[string]any{
				"text": "hello",
			},
		},
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("json.Marshal(DisplayBlocks) error = %v", err)
	}
	var decodedBlocks types.DisplayBlocks
	if err := json.Unmarshal(blocksJSON, &decodedBlocks); err != nil {
		t.Fatalf("json.Unmarshal(DisplayBlocks) error = %v", err)
	}
	if len(decodedBlocks) != len(blocks) {
		t.Fatalf("decoded blocks length = %d, want %d", len(decodedBlocks), len(blocks))
	}

	messages := []wire.WireMessage{
		wire.TurnBegin{
			TurnID: "turn-1",
			Input:  parts,
		},
		wire.TextDelta{
			TurnID: "turn-1",
			Delta:  "partial",
		},
		wire.SteerInput{
			Text:     "focus",
			Priority: "high",
		},
		wire.TurnEnd{
			TurnID:     "turn-1",
			StopReason: "stop",
			Output:     parts[:1],
			Usage: &types.TokenUsage{
				InputTokens:  1,
				OutputTokens: 2,
				TotalTokens:  3,
			},
		},
		wire.StepBegin{StepID: "step-1", Name: "plan", Description: "planning"},
		wire.StepInterrupted{StepID: "step-1", Reason: "pause"},
		wire.CompactionBegin{Trigger: "token_limit"},
		wire.CompactionEnd{Summary: "done", Content: parts[:1]},
		wire.MCPLoadingBegin{
			Snapshot: &wire.MCPServerSnapshot{
				Servers: []wire.MCPStatusSnapshot{
					{Name: "filesystem", Status: "ready"},
				},
			},
		},
		wire.MCPLoadingEnd{
			Snapshot: &wire.MCPServerSnapshot{
				Servers: []wire.MCPStatusSnapshot{
					{Name: "filesystem", Status: "ready"},
				},
			},
			DurationMS: 100,
		},
		wire.StatusUpdate{Status: "running", Message: "working"},
		wire.Notification{
			Level:   "info",
			Message: "done",
			Blocks:  blocks[:2],
		},
		wire.SubagentEvent{
			AgentID:   "agent-1",
			EventType: "started",
			Message:   "ok",
			Payload: map[string]any{
				"attempt": 1,
			},
		},
		wire.ApprovalRequest{
			ID:      "approval-1",
			Command: "echo",
		},
		wire.ApprovalResponse{
			RequestID: "approval-1",
			Approved:  true,
		},
		wire.ToolCallRequest{
			ID: "tool-req-1",
			ToolCall: types.ToolCall{
				ID:   "call-1",
				Name: "search",
				Arguments: map[string]any{
					"q": "x",
				},
			},
		},
		wire.ToolCallResult{
			ID: "tool-req-1",
			Result: types.ToolResult{
				ToolCallID: "call-1",
				Name:       "search",
				Value: types.ToolReturnValue{
					Value: map[string]any{
						"ok": true,
					},
				},
			},
		},
		wire.QuestionRequest{
			ID:     "question-1",
			Prompt: "choose",
			Items: []wire.QuestionItem{
				{
					ID:       "item-1",
					Question: "next",
					Options: []wire.QuestionOption{
						{Label: "Continue", Value: "continue"},
					},
				},
			},
		},
		wire.QuestionResponse{
			RequestID: "question-1",
			Answers: map[string]string{
				"item-1": "continue",
			},
		},
		wire.QuestionOption{Label: "Yes", Value: "yes"},
		wire.QuestionItem{ID: "item-2", Question: "done?"},
	}

	wirePath := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := wire.NewWireFile(wirePath)
	for i := range messages {
		envelope, err := wire.SerializeWireMessage(messages[i])
		if err != nil {
			t.Fatalf("SerializeWireMessage[%d]() error = %v", i, err)
		}
		decoded, err := wire.DeserializeWireMessageEnvelope(envelope)
		if err != nil {
			t.Fatalf("DeserializeWireMessageEnvelope[%d]() error = %v", i, err)
		}
		if reflect.TypeOf(decoded) != reflect.TypeOf(messages[i]) {
			t.Fatalf("decoded type[%d] = %T, want %T", i, decoded, messages[i])
		}

		if err := wireFile.AppendMessage(messages[i]); err != nil {
			t.Fatalf("WireFile.AppendMessage[%d]() error = %v", i, err)
		}
	}

	if wireFile.IsEmpty() {
		t.Fatal("wire file should not be empty after append")
	}
	iter, err := wireFile.IterRecords()
	if err != nil {
		t.Fatalf("WireFile.IterRecords() error = %v", err)
	}
	defer func() {
		if closeErr := iter.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()

	count := 0
	for iter.Next() {
		count++
		if _, err := iter.Record().Message(); err != nil {
			t.Fatalf("WireMessageRecord.Message() error = %v", err)
		}
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iterator err = %v", err)
	}
	if count != len(messages) {
		t.Fatalf("iterated record count = %d, want %d", count, len(messages))
	}
}
