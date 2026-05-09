package errors

import (
	stdErrors "errors"
	"fmt"
	"strings"
)

var (
	ErrConfigInvalid    = stdErrors.New("kimi: config invalid")
	ErrProviderNotFound = stdErrors.New("kimi: provider not found")
	ErrModelNotFound    = stdErrors.New("kimi: model not found")
	ErrToolNotFound     = stdErrors.New("kimi: tool not found")
	ErrToolRejected     = stdErrors.New("kimi: tool rejected")

	ErrMaxStepsReached  = stdErrors.New("kimi: max steps reached")
	ErrRunCancelled     = stdErrors.New("kimi: run cancelled")
	ErrLLMNotConfigured = stdErrors.New("kimi: llm not configured")
	ErrStepFailed       = stdErrors.New("kimi: step failed")

	// ErrBackendDisconnected is the canonical sentinel a SandboxBackend
	// returns (via fmt.Errorf("...: %w", ErrBackendDisconnected, ...))
	// when the backend has lost its execution substrate permanently —
	// e.g. a remote sandbox WebSocket disconnected, an SSH session
	// closed, the docker container died and we cannot reprovision
	// automatically.
	//
	// The Soul.Run loop short-circuits as soon as it sees an error chain
	// containing this sentinel and returns the wrapped error verbatim.
	// Tools are NOT given a chance to package this into a tool_result for
	// the LLM (which is the default behaviour for any other error coming
	// out of a tool's Execute) — feeding it to the model just produces a
	// retry storm because the LLM has no action that can revive a dead
	// substrate.
	//
	// Lives in this package (instead of pkg/kimi/tools/sandbox where the
	// SandboxBackend interface is defined) purely to avoid an import cycle
	// between internal/soul and tools/sandbox; the sandbox package
	// re-exports it as sandbox.ErrBackendDisconnected so users still
	// reference it from the package that conceptually owns it.
	ErrBackendDisconnected = stdErrors.New("kimi: backend disconnected")
)

// ConfigError describes one invalid config field/value error.
type ConfigError struct {
	Field  string
	Reason string
	Cause  error
}

func (e *ConfigError) Error() string {
	if e == nil {
		return ErrConfigInvalid.Error()
	}

	field := strings.TrimSpace(e.Field)
	reason := strings.TrimSpace(e.Reason)
	switch {
	case field != "" && reason != "":
		return fmt.Sprintf("config %s: %s", field, reason)
	case field != "":
		return fmt.Sprintf("config %s is invalid", field)
	case reason != "":
		return "config invalid: " + reason
	case e.Cause != nil:
		return "config invalid: " + e.Cause.Error()
	default:
		return ErrConfigInvalid.Error()
	}
}

func (e *ConfigError) Unwrap() error {
	if e == nil {
		return ErrConfigInvalid
	}
	if e.Cause == nil {
		return ErrConfigInvalid
	}
	return stdErrors.Join(ErrConfigInvalid, e.Cause)
}

// ToolError describes one tool-level failure with tool name context.
type ToolError struct {
	Name  string
	Cause error
}

func (e *ToolError) Error() string {
	if e == nil {
		return "tool error"
	}

	name := strings.TrimSpace(e.Name)
	cause := "failed"
	if e.Cause != nil {
		cause = strings.TrimSpace(e.Cause.Error())
		if cause == "" {
			cause = "failed"
		}
	}
	if name == "" {
		return "tool: " + cause
	}
	return fmt.Sprintf("tool %s: %s", name, cause)
}

func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// LLMError describes one provider/model call failure.
type LLMError struct {
	Provider   string
	StatusCode int
	Cause      error
}

func (e *LLMError) Error() string {
	if e == nil {
		return "llm error"
	}

	provider := strings.TrimSpace(e.Provider)
	if provider == "" {
		provider = "unknown"
	}

	message := fmt.Sprintf("llm provider %s", provider)
	if e.StatusCode > 0 {
		message = fmt.Sprintf("%s status=%d", message, e.StatusCode)
	}
	if e.Cause != nil {
		cause := strings.TrimSpace(e.Cause.Error())
		if cause != "" {
			message = message + ": " + cause
		}
	}
	return message
}

func (e *LLMError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
