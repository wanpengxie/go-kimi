package soul

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	// EngineName identifies the internal agent execution engine.
	EngineName = "soul"

	defaultMaxSteps           = 50
	defaultMaxStepRetries     = 3
	defaultStepRetryBaseDelay = 200 * time.Millisecond
	defaultStepRetryMaxDelay  = 5 * time.Second
	defaultSteerBufferSize    = 64
	defaultPlanReminderGap    = 3
)

// ToolExecutor executes one tool call.
type ToolExecutor interface {
	Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error)
}

// PreStepHook injects ephemeral messages before one step requests the model.
type PreStepHook func(ctx context.Context, history []Message) []Message

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

// StepRetryConfig controls retries for one failed step.
type StepRetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// PlanModeState stores plan mode runtime state used by pre-step reminders.
type PlanModeState struct {
	Active    bool
	SessionID string
	Slug      string
}

// SystemPromptTemplateData exposes variables used by system prompt templates.
type SystemPromptTemplateData struct {
	WorkDir       string
	DateTime      string
	Skills        string
	Yolo          bool
	PlanMode      bool
	PlanSessionID string
	PlanSlug      string
}

type steerRequest struct {
	TurnID   string
	Text     string
	Priority string
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
	stepRetry    StepRetryConfig
	steerCh      chan steerRequest

	stateMu                  sync.RWMutex
	activeTurnID             string
	preStepHooks             []PreStepHook
	templateData             SystemPromptTemplateData
	planMode                 PlanModeState
	currentStep              int
	lastPlanReminderStep     int
	yoloReminderInjectedTurn bool
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
		stepRetry: StepRetryConfig{
			MaxRetries: defaultMaxStepRetries,
			BaseDelay:  defaultStepRetryBaseDelay,
			MaxDelay:   defaultStepRetryMaxDelay,
		},
		steerCh:              make(chan steerRequest, defaultSteerBufferSize),
		lastPlanReminderStep: -defaultPlanReminderGap,
	}
	soul.approval = NewApprovalState(soul.emitApprovalRequest)
	soul.AddPreStepHook(soul.planModePreStepHook)
	soul.AddPreStepHook(soul.yoloModePreStepHook)
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

// SetStepRetryConfig configures step retry behavior.
func (s *Soul) SetStepRetryConfig(cfg StepRetryConfig) {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	s.stepRetry = normalizeStepRetryConfig(cfg)
	s.stateMu.Unlock()
}

// AddPreStepHook appends one hook that injects ephemeral messages before each step.
func (s *Soul) AddPreStepHook(hook PreStepHook) {
	if s == nil || hook == nil {
		return
	}
	s.stateMu.Lock()
	s.preStepHooks = append(s.preStepHooks, hook)
	s.stateMu.Unlock()
}

// ClearPreStepHooks removes all registered pre-step hooks.
func (s *Soul) ClearPreStepHooks() {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	s.preStepHooks = nil
	s.stateMu.Unlock()
}

// SetSystemPromptTemplateData sets explicit system prompt template data.
func (s *Soul) SetSystemPromptTemplateData(data SystemPromptTemplateData) {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	s.templateData = data
	s.stateMu.Unlock()
}

// SetPlanModeState updates plan mode state used by built-in pre-step reminders.
func (s *Soul) SetPlanModeState(state PlanModeState) {
	if s == nil {
		return
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	state.Slug = strings.TrimSpace(state.Slug)
	s.stateMu.Lock()
	s.planMode = state
	s.stateMu.Unlock()
}

// PlanModeState returns one snapshot of current plan mode state.
func (s *Soul) PlanModeState() PlanModeState {
	if s == nil {
		return PlanModeState{}
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.planMode
}

// Steer injects one extra user input into the currently running turn.
func (s *Soul) Steer(ctx context.Context, input string) error {
	if s == nil {
		return errors.New("soul steer: nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return errors.New("soul steer: input is empty")
	}

	s.stateMu.RLock()
	turnID := strings.TrimSpace(s.activeTurnID)
	steerCh := s.steerCh
	s.stateMu.RUnlock()
	if turnID == "" {
		return errors.New("soul steer: no active turn")
	}
	if steerCh == nil {
		return errors.New("soul steer: steering channel unavailable")
	}

	request := steerRequest{
		TurnID:   turnID,
		Text:     input,
		Priority: "normal",
	}
	select {
	case steerCh <- request:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("soul steer: %w", ctx.Err())
	}
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
	s.stepRetry = normalizeStepRetryConfig(s.stepRetry)
	s.compaction = normalizeCompactionConfig(s.compaction)
	if s.steerCh == nil {
		s.steerCh = make(chan steerRequest, defaultSteerBufferSize)
	}
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

func normalizeStepRetryConfig(cfg StepRetryConfig) StepRetryConfig {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = defaultStepRetryBaseDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = defaultStepRetryMaxDelay
	}
	if cfg.MaxDelay < cfg.BaseDelay {
		cfg.MaxDelay = cfg.BaseDelay
	}
	return cfg
}

func (s *Soul) beginTurnRuntime(turnID string) {
	if s == nil {
		return
	}

	s.stateMu.Lock()
	s.activeTurnID = strings.TrimSpace(turnID)
	s.currentStep = 0
	s.lastPlanReminderStep = -defaultPlanReminderGap
	s.yoloReminderInjectedTurn = false
	s.stateMu.Unlock()
}

func (s *Soul) endTurnRuntime(turnID string) {
	if s == nil {
		return
	}

	turnID = strings.TrimSpace(turnID)
	s.stateMu.Lock()
	if s.activeTurnID == turnID {
		s.activeTurnID = ""
	}
	s.stateMu.Unlock()
}

func (s *Soul) setCurrentStep(step int) {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	s.currentStep = step
	s.stateMu.Unlock()
}

func (s *Soul) preStepHooksSnapshot() []PreStepHook {
	if s == nil {
		return nil
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if len(s.preStepHooks) == 0 {
		return nil
	}
	out := make([]PreStepHook, len(s.preStepHooks))
	copy(out, s.preStepHooks)
	return out
}

func (s *Soul) stepRetryConfigSnapshot() StepRetryConfig {
	if s == nil {
		return StepRetryConfig{}
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return normalizeStepRetryConfig(s.stepRetry)
}

func (s *Soul) planModePreStepHook(_ context.Context, _ []Message) []Message {
	if s == nil {
		return nil
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if !s.planMode.Active {
		return nil
	}
	if s.currentStep-s.lastPlanReminderStep < defaultPlanReminderGap {
		return nil
	}
	s.lastPlanReminderStep = s.currentStep

	parts := []string{
		"Plan mode is active. Focus on planning quality, constraints, and explicit decisions.",
	}
	if s.planMode.Slug != "" {
		parts = append(parts, "Plan slug: "+s.planMode.Slug+".")
	}
	if s.planMode.SessionID != "" {
		parts = append(parts, "Plan session: "+s.planMode.SessionID+".")
	}
	return []Message{
		{
			Role: RoleSystem,
			Content: types.ContentParts{
				types.TextPart{Text: strings.Join(parts, " ")},
			},
		},
	}
}

func (s *Soul) yoloModePreStepHook(_ context.Context, _ []Message) []Message {
	if s == nil {
		return nil
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.yoloReminderInjectedTurn {
		return nil
	}
	if s.approval == nil || s.approval.IsYolo() {
		return nil
	}

	s.yoloReminderInjectedTurn = true
	return []Message{
		{
			Role: RoleSystem,
			Content: types.ContentParts{
				types.TextPart{Text: "Tool approvals are required in this turn. Explain intent before sensitive tool usage."},
			},
		},
	}
}

func (s *Soul) resolveTemplateData() SystemPromptTemplateData {
	if s == nil {
		return SystemPromptTemplateData{}
	}

	s.stateMu.RLock()
	data := s.templateData
	planState := s.planMode
	s.stateMu.RUnlock()

	data.WorkDir = strings.TrimSpace(data.WorkDir)
	if data.WorkDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			data.WorkDir = strings.TrimSpace(cwd)
		}
	}

	data.DateTime = time.Now().UTC().Format(time.RFC3339)
	data.PlanMode = planState.Active
	data.PlanSessionID = planState.SessionID
	data.PlanSlug = planState.Slug
	data.Yolo = s.IsYolo()

	data.Skills = strings.TrimSpace(data.Skills)
	if data.Skills == "" {
		defs := s.toolDefinitions()
		names := make([]string, 0, len(defs))
		for i := range defs {
			name := strings.TrimSpace(defs[i].Name)
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		data.Skills = strings.Join(names, ", ")
	}
	return data
}
