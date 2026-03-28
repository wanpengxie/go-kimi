package errors

import (
	stdErrors "errors"
	"testing"
)

func TestConfigErrorWrapsSentinelAndCause(t *testing.T) {
	t.Parallel()

	cause := stdErrors.New("bad type")
	err := &ConfigError{Field: "loop.max_turns", Reason: "must be >= 1", Cause: cause}
	if !stdErrors.Is(err, ErrConfigInvalid) {
		t.Fatalf("errors.Is(err, ErrConfigInvalid) = false, want true")
	}
	if !stdErrors.Is(err, cause) {
		t.Fatalf("errors.Is(err, cause) = false, want true")
	}
}

func TestToolErrorWrapsCause(t *testing.T) {
	t.Parallel()

	err := &ToolError{Name: "shell", Cause: ErrToolRejected}
	if !stdErrors.Is(err, ErrToolRejected) {
		t.Fatalf("errors.Is(err, ErrToolRejected) = false, want true")
	}
}

func TestLLMErrorWrapsCause(t *testing.T) {
	t.Parallel()

	err := &LLMError{Provider: "openai", StatusCode: 429, Cause: ErrStepFailed}
	if !stdErrors.Is(err, ErrStepFailed) {
		t.Fatalf("errors.Is(err, ErrStepFailed) = false, want true")
	}
}
