//go:build e2e_live

package e2e

import (
	"strings"
	"testing"
)

func liveRunOrSkip(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if liveIsEnvSkippableError(err) {
		t.Skipf("skip live e2e due env/provider constraint: %v", err)
	}
	t.Fatalf("run error = %v", err)
}

func liveIsEnvSkippableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	if strings.Contains(message, "resource_not_found_error") {
		return true
	}
	if strings.Contains(message, "not found the model") {
		return true
	}
	if strings.Contains(message, "permission denied") {
		return true
	}
	if strings.Contains(message, "status 401") || strings.Contains(message, "status 403") {
		return true
	}
	if strings.Contains(message, "status 429") {
		return true
	}
	if strings.Contains(message, "rate_limit") || strings.Contains(message, "rate limit") {
		return true
	}
	if strings.Contains(message, "engine_overloaded") || strings.Contains(message, "overloaded") {
		return true
	}
	return false
}
