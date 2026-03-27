package llm

import (
	"reflect"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	sharedtypes "github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestProviderTypeConstants(t *testing.T) {
	t.Parallel()

	cases := map[ProviderType]string{
		ProviderTypeMoonshot:    "moonshot",
		ProviderTypeOpenAI:      "openai",
		ProviderTypeAnthropic:   "anthropic",
		ProviderTypeGoogle:      "google",
		ProviderTypeAzureOpenAI: "azure_openai",
		ProviderTypeDeepSeek:    "deepseek",
	}

	for got, want := range cases {
		if string(got) != want {
			t.Fatalf("provider constant = %q, want %q", got, want)
		}
	}
}

func TestModelCapabilityConstants(t *testing.T) {
	t.Parallel()

	cases := map[ModelCapability]string{
		ModelCapabilityReasoning:  "reasoning",
		ModelCapabilityToolCall:   "tool_call",
		ModelCapabilityVision:     "vision",
		ModelCapabilityAudioInput: "audio_input",
		ModelCapabilityVideoInput: "video_input",
		ModelCapabilityJSONMode:   "json_mode",
		ModelCapabilityLongCtx:    "long_context",
	}

	for got, want := range cases {
		if string(got) != want {
			t.Fatalf("capability constant = %q, want %q", got, want)
		}
	}
}

func TestDeriveModelCapabilitiesUsesExplicitValues(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "custom",
		ContextWindow: 32768,
		Capabilities: []sharedtypes.ModelCapability{
			sharedtypes.ModelCapabilityReasoning,
			sharedtypes.ModelCapabilityToolCall,
			"function_calling",
			"CUSTOM_CAP",
			"",
		},
	}

	got := DeriveModelCapabilities(model)
	want := map[ModelCapability]bool{
		ModelCapabilityReasoning:      true,
		ModelCapabilityToolCall:       true,
		ModelCapability("custom_cap"): true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveModelCapabilities() = %#v, want %#v", got, want)
	}
}

func TestDeriveModelCapabilitiesFallsBackToHeuristics(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "kimi-k2-vision-json-audio-video",
		ContextWindow: 128000,
	}

	got := DeriveModelCapabilities(model)
	want := map[ModelCapability]bool{
		ModelCapabilityReasoning:  true,
		ModelCapabilityToolCall:   true,
		ModelCapabilityVision:     true,
		ModelCapabilityAudioInput: true,
		ModelCapabilityVideoInput: true,
		ModelCapabilityJSONMode:   true,
		ModelCapabilityLongCtx:    true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveModelCapabilities() = %#v, want %#v", got, want)
	}
}

func TestDeriveModelCapabilitiesAddsLongContextFromContextWindow(t *testing.T) {
	t.Parallel()

	model := config.LLMModel{
		Name:          "reason-only",
		ContextWindow: 256000,
		Capabilities: []sharedtypes.ModelCapability{
			sharedtypes.ModelCapabilityReasoning,
		},
	}

	got := DeriveModelCapabilities(model)
	want := map[ModelCapability]bool{
		ModelCapabilityReasoning: true,
		ModelCapabilityLongCtx:   true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveModelCapabilities() = %#v, want %#v", got, want)
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
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "blank", input: "   ", want: ""},
		{name: "kebab", input: "kimi-k2", want: "Kimi K2"},
		{name: "snake", input: " kimi_k2_vision ", want: "Kimi K2 Vision"},
		{name: "slash", input: "moonshot/v1", want: "Moonshot V1"},
		{name: "mixed", input: "gpt-4o-mini", want: "GPT 4O Mini"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ModelDisplayName(tc.input); got != tc.want {
				t.Fatalf("ModelDisplayName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
