package types_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestCrossPackageCoverageScenarioFromTypes(t *testing.T) {
	t.Parallel()

	if kimi.Version == "" {
		t.Fatal("kimi.Version must not be empty")
	}
	if soul.EngineName == "" {
		t.Fatal("soul.EngineName must not be empty")
	}

	cfg := config.NewDefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate() error = %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config", "config.toml")
	if err := config.SaveConfig(cfg, cfgPath); err != nil {
		t.Fatalf("config.SaveConfig() error = %v", err)
	}
	if _, err := config.LoadConfig(cfgPath); err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}
	if _, err := config.LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("config.LoadConfig(missing) expected error")
	}

	badCfg := cfg
	badCfg.Models[0].Provider = "missing-provider"
	if err := badCfg.Validate(); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Validate() error = %v, want provider does not exist error", err)
	}

	derived := llm.DeriveModelCapabilities(config.LLMModel{
		Name: "moonshot-k2-vision-json",
		Capabilities: []types.ModelCapability{
			"function_calling",
			"audio input",
		},
		ContextWindow: 128000,
	})
	if !derived[types.ModelCapabilityToolCall] || !derived[types.ModelCapabilityAudioInput] || !derived[types.ModelCapabilityLongCtx] {
		t.Fatalf("unexpected derived capabilities: %#v", derived)
	}
	if got := llm.ModelDisplayName(" kimi-k2_vl/reason "); got != "Kimi K2 VL Reason" {
		t.Fatalf("ModelDisplayName() = %q, want %q", got, "Kimi K2 VL Reason")
	}

	parts := types.ContentParts{
		types.TextPart{Text: "hello"},
		types.ThinkPart{Think: "plan"},
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
		t.Fatalf("decoded part count = %d, want %d", len(decodedParts), len(parts))
	}

	if err := json.Unmarshal([]byte(`{"type":"bad","tool_call":{"id":"1","name":"x"}}`), &types.ToolCallPart{}); err == nil {
		t.Fatal("ToolCallPart.UnmarshalJSON() unexpected type expected error")
	}
	result := types.ToolResult{
		ToolCallID: "call-1",
		Name:       "search",
		Value: types.ToolReturnValue{
			Value: map[string]any{"ok": true},
		},
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(ToolResult) error = %v", err)
	}
	var decodedResult types.ToolResult
	if err := json.Unmarshal(resultJSON, &decodedResult); err != nil {
		t.Fatalf("json.Unmarshal(ToolResult) error = %v", err)
	}
	if decodedResult.Name != result.Name {
		t.Fatalf("decoded result name = %q, want %q", decodedResult.Name, result.Name)
	}

	messages := []wire.WireMessage{
		wire.TurnBegin{TurnID: "turn-1", Input: parts[:2]},
		wire.ToolCallRequest{
			ID: "req-1",
			ToolCall: types.ToolCall{
				ID:   "call-1",
				Name: "search",
			},
		},
		wire.Notification{
			Level:   "info",
			Message: "hello",
			Blocks: types.DisplayBlocks{
				types.BriefDisplayBlock{Text: "summary"},
			},
		},
	}
	wirePath := filepath.Join(t.TempDir(), "wire", "events.jsonl")
	wireFile := wire.NewWireFile(wirePath)
	for i := range messages {
		envelope, err := wire.SerializeWireMessage(messages[i])
		if err != nil {
			t.Fatalf("SerializeWireMessage[%d]() error = %v", i, err)
		}
		if _, err := wire.DeserializeWireMessageEnvelope(envelope); err != nil {
			t.Fatalf("DeserializeWireMessageEnvelope[%d]() error = %v", i, err)
		}
		if err := wireFile.AppendMessage(messages[i]); err != nil {
			t.Fatalf("WireFile.AppendMessage[%d]() error = %v", i, err)
		}
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
			t.Fatalf("record.Message() error = %v", err)
		}
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iterator err = %v", err)
	}
	if count != len(messages) {
		t.Fatalf("record count = %d, want %d", count, len(messages))
	}
}
