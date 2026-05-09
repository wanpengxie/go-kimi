package soul

import (
	"context"
	"errors"
	"fmt"
	"testing"

	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

// Verifies the loop-level short-circuit: when a tool returns an error chain
// containing ErrBackendDisconnected, executeOneTool MUST surface that as a
// Go error instead of packaging it into a tool_result. Otherwise the agent
// loop hands the error back to the LLM, which has no way to recover.
func TestExecuteOneToolPropagatesBackendDisconnect(t *testing.T) {
	t.Parallel()

	disconnect := fmt.Errorf("hand WebSocket lost: %w", kimierrors.ErrBackendDisconnected)

	soul := newDisconnectHarness(executorFunc(func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
		return types.ToolResult{}, disconnect
	}))

	res, err := soul.executeOneTool(context.Background(), types.ToolCall{ID: "t-1", Name: "shell"})
	if err == nil {
		t.Fatalf("expected Go error to surface, got tool result: %+v", res)
	}
	if !errors.Is(err, kimierrors.ErrBackendDisconnected) {
		t.Errorf("error chain lost sentinel: %v", err)
	}
	if res.IsError {
		t.Errorf("when sentinel propagates, result must be empty (not packaged); got IsError=true")
	}
}

// Inverse invariant: any non-sentinel tool error MUST still flow through
// as a tool_result with IsError=true so the LLM can react.
func TestExecuteOneToolKeepsRecoverableErrorsAsToolResult(t *testing.T) {
	t.Parallel()

	plainErr := errors.New("file not found: /workspace/missing.txt")

	soul := newDisconnectHarness(executorFunc(func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
		return types.ToolResult{}, plainErr
	}))

	res, err := soul.executeOneTool(context.Background(), types.ToolCall{ID: "t-1", Name: "read_file"})
	if err != nil {
		t.Fatalf("recoverable err must NOT propagate as Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("recoverable err must produce ToolResult with IsError=true")
	}
}

func TestShouldRetryStepErrorSkipsBackendDisconnect(t *testing.T) {
	t.Parallel()
	disconnect := fmt.Errorf("tool %q: %w", "shell", kimierrors.ErrBackendDisconnected)
	if shouldRetryStepError(disconnect) {
		t.Errorf("shouldRetryStepError must return false for ErrBackendDisconnected chain")
	}
}

// End-to-end through executeTools: the goroutine fanout must aggregate the
// sentinel and short-circuit out of the dispatcher with the chain intact.
// Caller (loop.Run) then sees errors.Is(retErr, ErrBackendDisconnected) and
// exits the run loop without retries.
func TestExecuteToolsAggregatesBackendDisconnect(t *testing.T) {
	t.Parallel()

	disconnect := fmt.Errorf("ws read EOF: %w", kimierrors.ErrBackendDisconnected)

	soul := newDisconnectHarness(executorFunc(func(_ context.Context, _ types.ToolCall) (types.ToolResult, error) {
		return types.ToolResult{}, disconnect
	}))

	calls := []types.ToolCall{
		{ID: "c-1", Name: "shell"},
		{ID: "c-2", Name: "shell"},
	}
	results, err := soul.executeTools(context.Background(), calls)
	if err == nil {
		t.Fatalf("expected sentinel to surface")
	}
	if !errors.Is(err, kimierrors.ErrBackendDisconnected) {
		t.Errorf("sentinel chain lost from executeTools: %v", err)
	}
	if results != nil {
		t.Errorf("results must be discarded when sentinel fires; got %+v", results)
	}
}

// newDisconnectHarness builds the smallest Soul shell that can dispatch
// tool calls against the supplied executor. Wire emitter is a noop so the
// test focuses purely on the error path.
func newDisconnectHarness(exec executorFunc) *Soul {
	return &Soul{
		registry: mockRegistry{
			executors: map[string]ToolExecutor{
				"shell":     exec,
				"read_file": exec,
			},
		},
		wire: emitterFunc(func(_ wire.WireMessage) error { return nil }),
	}
}
