package soul

import (
	"context"
	"errors"
	"strings"

	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
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

// Soul coordinates provider calls, context persistence and tool execution.
type Soul struct {
	provider     llm.ChatProvider
	context      *SoulContext
	registry     ToolRegistry
	wire         wire.Emitter
	approval     *ApprovalState
	compactor    Compactor
	compaction   CompactionConfig
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

	soul := &Soul{
		provider:     provider,
		context:      ctx,
		registry:     registry,
		wire:         w,
		compactor:    &SimpleCompaction{},
		compaction:   defaultCompactionConfig(),
		systemPrompt: strings.TrimSpace(systemPrompt),
		maxSteps:     defaultMaxSteps,
	}
	soul.approval = NewApprovalState(soul.emitApprovalRequest)
	return soul
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

// ToolRegistry returns the current tool registry.
func (s *Soul) ToolRegistry() ToolRegistry {
	if s == nil {
		return nil
	}
	return s.registry
}

// SetToolRegistry replaces the current tool registry.
func (s *Soul) SetToolRegistry(registry ToolRegistry) {
	if s == nil {
		return
	}
	s.registry = registry
}

// SetProvider replaces the current chat provider.
func (s *Soul) SetProvider(provider llm.ChatProvider) {
	if s == nil {
		return
	}
	s.provider = provider
}

// SetYolo toggles yolo mode for tool approval.
func (s *Soul) SetYolo(v bool) {
	if s == nil || s.approval == nil {
		return
	}
	s.approval.SetYolo(v)
}

// IsYolo reports the current yolo mode.
func (s *Soul) IsYolo() bool {
	if s == nil || s.approval == nil {
		return true
	}
	return s.approval.IsYolo()
}

// RespondApproval resolves one pending approval request.
func (s *Soul) RespondApproval(requestID string, decision ApprovalDecision, feedback string) error {
	if s == nil || s.approval == nil {
		return errors.New("soul: approval is unavailable")
	}
	return s.approval.Respond(requestID, decision, feedback)
}

// SetApprovalRuntime configures one optional centralized approval runtime for this soul.
func (s *Soul) SetApprovalRuntime(runtime *approvalruntime.ApprovalRuntime, source approvalruntime.ApprovalSource) {
	if s == nil {
		return
	}
	if s.approval == nil {
		s.approval = NewApprovalState(s.emitApprovalRequest)
	}
	s.approval.SetRuntime(runtime, source)
}

// SetCompactor replaces the context compactor used by automatic compaction.
func (s *Soul) SetCompactor(compactor Compactor) {
	if s == nil {
		return
	}
	s.compactor = compactor
}

// SetCompactionConfig configures automatic context compaction behavior.
func (s *Soul) SetCompactionConfig(cfg CompactionConfig) {
	if s == nil {
		return
	}
	s.compaction = normalizeCompactionConfig(cfg)
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
	s.compaction = normalizeCompactionConfig(s.compaction)
	if s.approval == nil {
		s.approval = NewApprovalState(s.emitApprovalRequest)
	}
	return nil
}

func (s *Soul) emitApprovalRequest(request *ApprovalRequest) {
	if request == nil {
		return
	}
	_ = s.emit(wire.ApprovalRequest{
		ID:          request.ID,
		Kind:        "tool_call",
		Title:       request.Action,
		Description: request.Description,
	})
}
