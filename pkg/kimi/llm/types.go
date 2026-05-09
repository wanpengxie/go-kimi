package llm

import (
	"strings"
	"unicode"

	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

// ProviderType identifies a model provider backend.
type ProviderType string

const (
	ProviderTypeKimi         ProviderType = "kimi"
	ProviderTypeMoonshot     ProviderType = "moonshot"
	ProviderTypeOpenAI       ProviderType = "openai"
	ProviderTypeAnthropic    ProviderType = "anthropic"
	ProviderTypeGemini       ProviderType = "gemini"
	ProviderTypeGoogle       ProviderType = "google"
	ProviderTypeAzureOpenAI  ProviderType = "azure_openai"
	ProviderTypeDeepSeek     ProviderType = "deepseek"
	ProviderTypeEcho         ProviderType = "echo"
	ProviderTypeScriptedEcho ProviderType = "scripted_echo"
)

const longContextThreshold = 128000

// LLM is the runtime model selection bundle.
type LLM struct {
	ChatProvider   ChatProvider
	MaxContextSize int
	Capabilities   map[types.ModelCapability]bool
}

// DeriveModelCapabilities derives a capability set from model metadata.
func DeriveModelCapabilities(model config.LLMModel) map[types.ModelCapability]bool {
	capabilities := make(map[types.ModelCapability]bool)

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
			capabilities[types.ModelCapabilityReasoning] = true
			capabilities[types.ModelCapabilityToolCall] = true
		}
		if strings.Contains(name, "vision") || strings.Contains(name, "vl") {
			capabilities[types.ModelCapabilityVision] = true
		}
		if strings.Contains(name, "audio") {
			capabilities[types.ModelCapabilityAudioInput] = true
		}
		if strings.Contains(name, "video") {
			capabilities[types.ModelCapabilityVideoInput] = true
		}
		if strings.Contains(name, "json") || strings.Contains(name, "structured") {
			capabilities[types.ModelCapabilityJSONMode] = true
		}
	}

	if model.ContextWindow >= longContextThreshold {
		capabilities[types.ModelCapabilityLongCtx] = true
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

func normalizeCapability(raw string) types.ModelCapability {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch normalized {
	case "":
		return ""
	case "reasoning":
		return types.ModelCapabilityReasoning
	case "toolcall", "tool_call", "function_call", "function_calling", "function_calls":
		return types.ModelCapabilityToolCall
	case "vision":
		return types.ModelCapabilityVision
	case "audio", "audio_input":
		return types.ModelCapabilityAudioInput
	case "video", "video_input":
		return types.ModelCapabilityVideoInput
	case "json", "json_mode":
		return types.ModelCapabilityJSONMode
	case "long_context", "long_ctx", "longcontext":
		return types.ModelCapabilityLongCtx
	default:
		return types.ModelCapability(normalized)
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
