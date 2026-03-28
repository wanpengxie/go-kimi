package llm

import "strings"

// ThinkingEffort controls provider reasoning intensity.
type ThinkingEffort string

const (
	ThinkingOff    ThinkingEffort = "off"
	ThinkingLow    ThinkingEffort = "low"
	ThinkingMedium ThinkingEffort = "medium"
	ThinkingHigh   ThinkingEffort = "high"
)

// ThinkingProvider is an optional provider capability that supports thinking effort control.
type ThinkingProvider interface {
	ChatProvider
	WithThinking(effort ThinkingEffort) ChatProvider
}

// NormalizeThinkingEffort normalizes raw effort values to supported levels.
func NormalizeThinkingEffort(effort ThinkingEffort) ThinkingEffort {
	switch strings.ToLower(strings.TrimSpace(string(effort))) {
	case string(ThinkingLow):
		return ThinkingLow
	case string(ThinkingMedium):
		return ThinkingMedium
	case string(ThinkingHigh):
		return ThinkingHigh
	default:
		return ThinkingOff
	}
}

// WithThinking applies thinking effort when the provider supports ThinkingProvider.
func WithThinking(provider ChatProvider, effort ThinkingEffort) ChatProvider {
	if provider == nil {
		return nil
	}

	thinkingProvider, ok := provider.(ThinkingProvider)
	if !ok {
		return provider
	}

	next := thinkingProvider.WithThinking(NormalizeThinkingEffort(effort))
	if next == nil {
		return provider
	}
	return next
}
