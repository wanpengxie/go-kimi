package types

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
