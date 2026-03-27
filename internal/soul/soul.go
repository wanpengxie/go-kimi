package soul

import (
	"context"
	"errors"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	// EngineName identifies the internal agent execution engine.
	EngineName = "soul"

	defaultMaxSteps = 50
)

// ToolExecutor executes one tool call.
type ToolExecutor interface {
	Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error)
}

// ToolRegistry provides model-facing definitions and call executors.
type ToolRegistry interface {
	Definitions() []llm.ToolDefinition
	Executor(name string) (ToolExecutor, bool)
}

// StepResult is one step execution outcome.
type StepResult struct {
	Content     types.ContentParts
	ToolCalls   []types.ToolCall
	ToolResults []types.ToolResult
	Usage       types.TokenUsage
}

// ApprovalState is a placeholder for M2-D approval integration.
type ApprovalState struct{}

// Soul coordinates provider calls, context persistence and tool execution.
type Soul struct {
	provider     llm.ChatProvider
	context      *SoulContext
	registry     ToolRegistry
	wire         wire.Emitter
	approval     *ApprovalState
	systemPrompt string
	maxSteps     int
}

// NewSoul creates one soul runtime with sane defaults.
func NewSoul(
	provider llm.ChatProvider,
	ctx *SoulContext,
	registry ToolRegistry,
	w wire.Emitter,
	systemPrompt string,
) *Soul {
	if w == nil {
		w = wire.NoopEmitter{}
	}

	return &Soul{
		provider:     provider,
		context:      ctx,
		registry:     registry,
		wire:         w,
		systemPrompt: strings.TrimSpace(systemPrompt),
		maxSteps:     defaultMaxSteps,
	}
}

// SetMaxSteps overrides the turn loop limit.
func (s *Soul) SetMaxSteps(maxSteps int) {
	if s == nil {
		return
	}
	if maxSteps < 1 {
		s.maxSteps = 1
		return
	}
	s.maxSteps = maxSteps
}

func (s *Soul) ensureReady() error {
	if s == nil {
		return errors.New("soul: nil")
	}
	if s.provider == nil {
		return errors.New("soul: nil provider")
	}
	if s.context == nil {
		return errors.New("soul: nil context")
	}
	if s.wire == nil {
		s.wire = wire.NoopEmitter{}
	}
	if s.maxSteps < 1 {
		s.maxSteps = defaultMaxSteps
	}
	// M2-D will activate approval checks in step().
	_ = s.approval
	return nil
}
