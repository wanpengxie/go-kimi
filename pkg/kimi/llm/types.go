package llm

import (
	"strings"
	"unicode"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
)

// ProviderType identifies a model provider backend.
type ProviderType string

const (
	ProviderTypeMoonshot    ProviderType = "moonshot"
	ProviderTypeOpenAI      ProviderType = "openai"
	ProviderTypeAnthropic   ProviderType = "anthropic"
	ProviderTypeGoogle      ProviderType = "google"
	ProviderTypeAzureOpenAI ProviderType = "azure_openai"
	ProviderTypeDeepSeek    ProviderType = "deepseek"
)

// ModelCapability describes an optional model feature.
type ModelCapability string

const (
	ModelCapabilityReasoning  ModelCapability = "reasoning"
	ModelCapabilityToolCall   ModelCapability = "tool_call"
	ModelCapabilityVision     ModelCapability = "vision"
	ModelCapabilityAudioInput ModelCapability = "audio_input"
	ModelCapabilityVideoInput ModelCapability = "video_input"
	ModelCapabilityJSONMode   ModelCapability = "json_mode"
	ModelCapabilityLongCtx    ModelCapability = "long_context"
)

const longContextThreshold = 128000

// LLM is the runtime model selection bundle.
type LLM struct {
	ChatProvider   ChatProvider
	MaxContextSize int
	Capabilities   map[ModelCapability]bool
}

// DeriveModelCapabilities derives a capability set from model metadata.
func DeriveModelCapabilities(model config.LLMModel) map[ModelCapability]bool {
	capabilities := make(map[ModelCapability]bool)

	for _, capability := range model.Capabilities {
		normalized := normalizeCapability(string(capability))
		if normalized == "" {
			continue
		}
		capabilities[normalized] = true
	}

	name := strings.ToLower(strings.TrimSpace(model.Name))
	if len(capabilities) == 0 {
		if strings.Contains(name, "k2") || strings.Contains(name, "reason") {
			capabilities[ModelCapabilityReasoning] = true
			capabilities[ModelCapabilityToolCall] = true
		}
		if strings.Contains(name, "vision") || strings.Contains(name, "vl") {
			capabilities[ModelCapabilityVision] = true
		}
		if strings.Contains(name, "audio") {
			capabilities[ModelCapabilityAudioInput] = true
		}
		if strings.Contains(name, "video") {
			capabilities[ModelCapabilityVideoInput] = true
		}
		if strings.Contains(name, "json") || strings.Contains(name, "structured") {
			capabilities[ModelCapabilityJSONMode] = true
		}
	}

	if model.ContextWindow >= longContextThreshold {
		capabilities[ModelCapabilityLongCtx] = true
	}

	return capabilities
}

// ModelDisplayName converts a model identifier into a human-readable label.
func ModelDisplayName(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}

	parts := strings.FieldsFunc(modelName, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '_' || r == '/'
	})
	if len(parts) == 0 {
		return ""
	}

	for i, part := range parts {
		parts[i] = displayToken(part)
	}

	return strings.Join(parts, " ")
}

func normalizeCapability(raw string) ModelCapability {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch normalized {
	case "":
		return ""
	case "reasoning":
		return ModelCapabilityReasoning
	case "toolcall", "tool_call", "function_call", "function_calling", "function_calls":
		return ModelCapabilityToolCall
	case "vision":
		return ModelCapabilityVision
	case "audio", "audio_input":
		return ModelCapabilityAudioInput
	case "video", "video_input":
		return ModelCapabilityVideoInput
	case "json", "json_mode":
		return ModelCapabilityJSONMode
	case "long_context", "long_ctx", "longcontext":
		return ModelCapabilityLongCtx
	default:
		return ModelCapability(normalized)
	}
}

func displayToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 3 {
		return strings.ToUpper(token)
	}
	return strings.ToUpper(token[:1]) + strings.ToLower(token[1:])
}
