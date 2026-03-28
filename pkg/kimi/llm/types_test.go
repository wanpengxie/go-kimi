package llm

import (
	"context"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

type stubProvider struct {
	modelName string
	effort    ThinkingEffort
}

func (s stubProvider) ModelName() string {
	return s.modelName
}

func (s stubProvider) WithModel(model string) ChatProvider {
	s.modelName = model
	return s
}

func (s stubProvider) WithThinking(effort ThinkingEffort) ChatProvider {
	s.effort = effort
	return s
}

func (s stubProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}

func (s stubProvider) ChatStream(_ context.Context, _ ChatRequest) (<-chan ChatEvent, error) {
	ch := make(chan ChatEvent)
	close(ch)
	return ch, nil
}

var _ ChatProvider = stubProvider{}

func TestProviderTypeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  ProviderType
		want ProviderType
	}{
		{name: "kimi", got: ProviderTypeKimi, want: "kimi"},
		{name: "moonshot", got: ProviderTypeMoonshot, want: "moonshot"},
		{name: "openai", got: ProviderTypeOpenAI, want: "openai"},
		{name: "anthropic", got: ProviderTypeAnthropic, want: "anthropic"},
		{name: "gemini", got: ProviderTypeGemini, want: "gemini"},
		{name: "google", got: ProviderTypeGoogle, want: "google"},
		{name: "azure_openai", got: ProviderTypeAzureOpenAI, want: "azure_openai"},
		{name: "deepseek", got: ProviderTypeDeepSeek, want: "deepseek"},
		{name: "echo", got: ProviderTypeEcho, want: "echo"},
		{name: "scripted_echo", got: ProviderTypeScriptedEcho, want: "scripted_echo"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("provider type = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestModelCapabilityConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  types.ModelCapability
		want types.ModelCapability
	}{
		{name: "reasoning", got: types.ModelCapabilityReasoning, want: "reasoning"},
		{name: "tool_call", got: types.ModelCapabilityToolCall, want: "tool_call"},
		{name: "vision", got: types.ModelCapabilityVision, want: "vision"},
		{name: "audio_input", got: types.ModelCapabilityAudioInput, want: "audio_input"},
		{name: "video_input", got: types.ModelCapabilityVideoInput, want: "video_input"},
		{name: "json_mode", got: types.ModelCapabilityJSONMode, want: "json_mode"},
		{name: "long_context", got: types.ModelCapabilityLongCtx, want: "long_context"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("model capability = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestLLMStruct(t *testing.T) {
	t.Parallel()

	provider := stubProvider{modelName: "kimi-k2"}
	capabilities := map[types.ModelCapability]bool{
		types.ModelCapabilityReasoning: true,
		types.ModelCapabilityToolCall:  true,
	}

	model := LLM{
		ChatProvider:   provider,
		MaxContextSize: 128000,
		Capabilities:   capabilities,
	}

	if model.ChatProvider.ModelName() != "kimi-k2" {
		t.Fatalf("ChatProvider.ModelName() = %q, want %q", model.ChatProvider.ModelName(), "kimi-k2")
	}
	if model.MaxContextSize != 128000 {
		t.Fatalf("MaxContextSize = %d, want %d", model.MaxContextSize, 128000)
	}
	if !model.Capabilities[types.ModelCapabilityReasoning] {
		t.Fatal("reasoning capability should be true")
	}
	if !model.Capabilities[types.ModelCapabilityToolCall] {
		t.Fatal("tool_call capability should be true")
	}
}

func TestDeriveModelCapabilitiesFromExplicitModelCapabilities(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "kimi-k2-vision",
		ContextWindow: 32000,
		Capabilities: []types.ModelCapability{
			" reasoning ",
			"tool-call",
			"json",
			"custom_capability",
			"",
			"   ",
		},
	}

	got := DeriveModelCapabilities(model)

	want := map[types.ModelCapability]bool{
		types.ModelCapabilityReasoning:             true,
		types.ModelCapabilityToolCall:              true,
		types.ModelCapabilityJSONMode:              true,
		types.ModelCapability("custom_capability"): true,
	}

	assertCapabilitySet(t, got, want)
	if got[types.ModelCapabilityVision] {
		t.Fatal("vision should not be auto-derived when explicit capabilities are present")
	}
}

func TestDeriveModelCapabilitiesNormalizesAliases(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name: "moonshot-v1",
		Capabilities: []types.ModelCapability{
			"function_calling",
			"long_ctx",
			"audio input",
		},
	}

	got := DeriveModelCapabilities(model)
	want := map[types.ModelCapability]bool{
		types.ModelCapabilityToolCall:   true,
		types.ModelCapabilityLongCtx:    true,
		types.ModelCapabilityAudioInput: true,
	}

	assertCapabilitySet(t, got, want)
}

func TestDeriveModelCapabilitiesHeuristics(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "kimi-k2-vision-audio-video-json",
		ContextWindow: 128000,
	}

	got := DeriveModelCapabilities(model)
	want := map[types.ModelCapability]bool{
		types.ModelCapabilityReasoning:  true,
		types.ModelCapabilityToolCall:   true,
		types.ModelCapabilityVision:     true,
		types.ModelCapabilityAudioInput: true,
		types.ModelCapabilityVideoInput: true,
		types.ModelCapabilityJSONMode:   true,
		types.ModelCapabilityLongCtx:    true,
	}

	assertCapabilitySet(t, got, want)
}

func TestDeriveModelCapabilitiesLongContextFromContextWindow(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "moonshot-v1",
		ContextWindow: 128000,
		Capabilities: []types.ModelCapability{
			"tool_call",
		},
	}

	got := DeriveModelCapabilities(model)

	if !got[types.ModelCapabilityLongCtx] {
		t.Fatal("long_context should be derived when context_window >= 128000")
	}
	if !got[types.ModelCapabilityToolCall] {
		t.Fatal("tool_call should stay in capability set")
	}
}

func TestDeriveModelCapabilitiesEmptyInput(t *testing.T) {
	t.Parallel()

	got := DeriveModelCapabilities(config.LLMModel{})
	if got == nil {
		t.Fatal("DeriveModelCapabilities() returned nil map")
	}
	if len(got) != 0 {
		t.Fatalf("len(DeriveModelCapabilities()) = %d, want 0", len(got))
	}
}

func TestModelDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{
			name:     "empty",
			model:    "  ",
			expected: "",
		},
		{
			name:     "mixed separators",
			model:    " kimi-k2_vl/reason ",
			expected: "Kimi K2 VL Reason",
		},
		{
			name:     "short token uppercased",
			model:    "gpt-o1",
			expected: "GPT O1",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ModelDisplayName(tc.model)
			if got != tc.expected {
				t.Fatalf("ModelDisplayName(%q) = %q, want %q", tc.model, got, tc.expected)
			}
		})
	}
}

func assertCapabilitySet(t *testing.T, got map[types.ModelCapability]bool, want map[types.ModelCapability]bool) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("capability set size = %d, want %d: got=%v", len(got), len(want), got)
	}
	for capability := range want {
		if !got[capability] {
			t.Fatalf("expected capability %q in set: got=%v", capability, got)
		}
	}
}
