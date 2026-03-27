package llm

// ChatProvider defines the provider-facing chat abstraction.
type ChatProvider interface {
	ModelName() string
	WithThinking(effort string) ChatProvider
}
