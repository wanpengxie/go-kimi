//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestScriptedConfigLoadSaveRoundTrip(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Providers = []config.LLMProvider{
		{
			Name:    "scripted-echo",
			Type:    "scripted_echo",
			BaseURL: "http://scripted.local/echo",
		},
	}
	cfg.Models = []config.LLMModel{
		{
			Name:            "echo-v1",
			Provider:        "scripted-echo",
			ContextWindow:   4096,
			MaxOutputTokens: 512,
			Capabilities: []types.ModelCapability{
				types.ModelCapabilityReasoning,
				types.ModelCapabilityToolCall,
			},
		},
	}
	cfg.DefaultProvider = "scripted-echo"
	cfg.DefaultModel = "echo-v1"

	path := filepath.Join(t.TempDir(), "scripted-config.toml")
	if err := config.SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("loaded config differs from saved config\nloaded=%#v\nsaved=%#v", loaded, cfg)
	}
}

func TestScriptedWireMessageSerializationRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		msg  wire.WireMessage
	}{
		{
			name: "turn_begin_with_parts",
			msg: wire.TurnBegin{
				TurnID: "turn-e2e-1",
				Input: types.ContentParts{
					types.TextPart{Text: "hello"},
					types.ThinkPart{Think: "internal"},
				},
			},
		},
		{
			name: "tool_call_request",
			msg: wire.ToolCallRequest{
				ID: "req-1",
				ToolCall: types.ToolCall{
					ID:   "call-1",
					Name: "echo",
					Arguments: map[string]any{
						"message": "hello",
					},
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			envelope, err := wire.SerializeWireMessage(tc.msg)
			if err != nil {
				t.Fatalf("SerializeWireMessage() error = %v", err)
			}

			rawEnvelope, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("json.Marshal(envelope) error = %v", err)
			}

			var payload map[string]any
			if err := json.Unmarshal(rawEnvelope, &payload); err != nil {
				t.Fatalf("json.Unmarshal(envelope map) error = %v", err)
			}

			decoded, err := wire.DeserializeWireMessage(payload)
			if err != nil {
				t.Fatalf("DeserializeWireMessage() error = %v", err)
			}

			assertWireMessageEqual(t, decoded, tc.msg)
		})
	}
}

func assertWireMessageEqual(t *testing.T, got wire.WireMessage, want wire.WireMessage) {
	t.Helper()

	if reflect.TypeOf(got) != reflect.TypeOf(want) {
		t.Fatalf("message type = %T, want %T", got, want)
	}

	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(got) error = %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal(want) error = %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("message json mismatch\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}
