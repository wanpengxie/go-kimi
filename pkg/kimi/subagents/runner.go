package subagents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wanpengxie/go-kimi/internal/soul"
	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

const defaultSubagentType = "general-purpose"

const defaultSummaryContinuationPrompt = "The previous response is too brief. Provide a fuller final summary (at least 200 characters) focusing on concrete actions, outcomes, and caveats. Do not call tools."

var foregroundAgentSequence uint64

// ForegroundRunRequest defines one synchronous subagent run request.
type ForegroundRunRequest struct {
	AgentID          string
	SubagentType     string
	Prompt           string
	ModelOverride    string
	Background       bool
	BackgroundTaskID string
}

// RunnerDeps defines required dependencies for ForegroundSubagentRunner.
type RunnerDeps struct {
	Market          *LaborMarket
	Store           *SubagentStore
	Provider        llm.ChatProvider
	ApprovalRuntime *approvalruntime.ApprovalRuntime
	ParentRegistry  soul.ToolRegistry
	SystemPrompt    string
	WorkDir         string
	WireEmitter     wire.Emitter
	// SummaryContinuationMinChars enables one summary continuation run when output
	// text is shorter than this threshold. Values <= 0 disable continuation.
	SummaryContinuationMinChars int
}

// ForegroundSubagentRunner executes one subagent run synchronously.
type ForegroundSubagentRunner struct {
	deps RunnerDeps
}

// NewForegroundSubagentRunner builds one foreground runner.
func NewForegroundSubagentRunner(deps RunnerDeps) *ForegroundSubagentRunner {
	deps.SystemPrompt = strings.TrimSpace(deps.SystemPrompt)
	deps.WorkDir = strings.TrimSpace(deps.WorkDir)
	return &ForegroundSubagentRunner{deps: deps}
}

// Run executes one foreground subagent run and persists lifecycle updates.
func (r *ForegroundSubagentRunner) Run(ctx context.Context, req ForegroundRunRequest) (types.ToolReturnValue, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateDeps(); err != nil {
		return types.ToolReturnValue{}, err
	}

	normalizedReq, err := normalizeRunRequest(req)
	if err != nil {
		return types.ToolReturnValue{}, err
	}

	if normalizedReq.AgentID == "" {
		return r.runNew(ctx, normalizedReq)
	}
	return r.runExisting(ctx, normalizedReq)
}

func (r *ForegroundSubagentRunner) validateDeps() error {
	if r == nil {
		return errors.New("subagents: nil foreground runner")
	}
	if r.deps.Market == nil {
		return errors.New("subagents: nil labor market")
	}
	if r.deps.Store == nil {
		return errors.New("subagents: nil subagent store")
	}
	if r.deps.Provider == nil {
		return errors.New("subagents: nil provider")
	}
	return nil
}

func normalizeRunRequest(req ForegroundRunRequest) (ForegroundRunRequest, error) {
	normalized := ForegroundRunRequest{
		AgentID:          strings.TrimSpace(req.AgentID),
		SubagentType:     strings.TrimSpace(req.SubagentType),
		Prompt:           strings.TrimSpace(req.Prompt),
		ModelOverride:    strings.TrimSpace(req.ModelOverride),
		Background:       req.Background,
		BackgroundTaskID: strings.TrimSpace(req.BackgroundTaskID),
	}
	if normalized.Prompt == "" {
		return ForegroundRunRequest{}, errors.New("subagents: prompt is required")
	}
	return normalized, nil
}

func (r *ForegroundSubagentRunner) runNew(ctx context.Context, req ForegroundRunRequest) (types.ToolReturnValue, error) {
	subagentType := req.SubagentType
	if subagentType == "" {
		subagentType = defaultSubagentType
	}

	def, err := r.deps.Market.Require(subagentType)
	if err != nil {
		return types.ToolReturnValue{}, err
	}

	now := nowUnixSeconds()
	effectiveModel := resolveEffectiveModel(req.ModelOverride, def.DefaultModel, r.deps.Provider.ModelName())
	record := &AgentInstanceRecord{
		AgentID:      newForegroundAgentID(),
		SubagentType: def.Name,
		Status:       runningStatusFor(req.Background),
		Description:  req.Prompt,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastTaskID:   req.BackgroundTaskID,
		LaunchSpec: AgentLaunchSpec{
			ModelOverride:  req.ModelOverride,
			EffectiveModel: effectiveModel,
			CreatedAt:      now,
		},
	}
	if record.LaunchSpec.AgentID == "" {
		record.LaunchSpec.AgentID = record.AgentID
	}
	if record.LaunchSpec.SubagentType == "" {
		record.LaunchSpec.SubagentType = record.SubagentType
	}

	if err := r.deps.Store.Create(record); err != nil {
		return types.ToolReturnValue{}, err
	}

	if err := r.deps.Store.Update(record); err != nil {
		return types.ToolReturnValue{}, err
	}

	return r.runWithRecord(ctx, req, def, record)
}

func (r *ForegroundSubagentRunner) runExisting(ctx context.Context, req ForegroundRunRequest) (types.ToolReturnValue, error) {
	record, err := r.deps.Store.Get(req.AgentID)
	if err != nil {
		return types.ToolReturnValue{}, err
	}

	if req.SubagentType != "" && req.SubagentType != record.SubagentType {
		return types.ToolReturnValue{}, fmt.Errorf(
			"subagents: request subagent_type %q does not match existing %q",
			req.SubagentType,
			record.SubagentType,
		)
	}
	if record.Status == StatusRunningForeground || record.Status == StatusRunningBackground {
		return types.ToolReturnValue{}, fmt.Errorf(
			"subagents: agent %q is already running (%s)",
			record.AgentID,
			record.Status,
		)
	}

	def, err := r.deps.Market.Require(record.SubagentType)
	if err != nil {
		return types.ToolReturnValue{}, err
	}

	now := nowUnixSeconds()
	record.Status = runningStatusFor(req.Background)
	record.Description = req.Prompt
	record.UpdatedAt = now
	if req.BackgroundTaskID != "" {
		record.LastTaskID = req.BackgroundTaskID
	}
	if record.LaunchSpec.AgentID == "" {
		record.LaunchSpec.AgentID = record.AgentID
	}
	if record.LaunchSpec.SubagentType == "" {
		record.LaunchSpec.SubagentType = record.SubagentType
	}
	if record.LaunchSpec.CreatedAt == 0 {
		record.LaunchSpec.CreatedAt = record.CreatedAt
		if record.LaunchSpec.CreatedAt == 0 {
			record.LaunchSpec.CreatedAt = now
		}
	}
	if req.ModelOverride != "" {
		record.LaunchSpec.ModelOverride = req.ModelOverride
		record.LaunchSpec.EffectiveModel = resolveEffectiveModel(req.ModelOverride, def.DefaultModel, r.deps.Provider.ModelName())
	} else if strings.TrimSpace(record.LaunchSpec.EffectiveModel) == "" {
		record.LaunchSpec.EffectiveModel = resolveEffectiveModel(record.LaunchSpec.ModelOverride, def.DefaultModel, r.deps.Provider.ModelName())
	}

	if err := r.deps.Store.Update(record); err != nil {
		return types.ToolReturnValue{}, err
	}

	return r.runWithRecord(ctx, req, def, record)
}

func (r *ForegroundSubagentRunner) runWithRecord(
	ctx context.Context,
	req ForegroundRunRequest,
	def *AgentTypeDefinition,
	record *AgentInstanceRecord,
) (types.ToolReturnValue, error) {
	runResult, outputWriter, runErr := r.executeSoulRun(ctx, req, def, record)
	finalStatus := StatusCompleted
	if runErr != nil {
		finalStatus = StatusFailed
	}

	record.Status = finalStatus
	record.UpdatedAt = nowUnixSeconds()
	if err := r.deps.Store.Update(record); err != nil {
		if runErr != nil {
			return types.ToolReturnValue{}, fmt.Errorf("%v; subagents: update status: %w", runErr, err)
		}
		return types.ToolReturnValue{}, fmt.Errorf("subagents: update status: %w", err)
	}

	if runErr != nil {
		return types.ToolReturnValue{}, runErr
	}
	return buildRunReturnValue(record, runResult, outputWriter), nil
}

func (r *ForegroundSubagentRunner) executeSoulRun(
	ctx context.Context,
	req ForegroundRunRequest,
	def *AgentTypeDefinition,
	record *AgentInstanceRecord,
) (soul.StepResult, *SubagentOutputWriter, error) {
	contextDir, err := r.agentContextDir(record.AgentID)
	if err != nil {
		return soul.StepResult{}, nil, err
	}

	outputWriter := newSubagentOutputWriter()
	engine, err := Build(def, BuildConfig{
		Provider:       r.deps.Provider.WithModel(record.LaunchSpec.EffectiveModel),
		SystemPrompt:   r.deps.SystemPrompt,
		WorkDir:        r.deps.WorkDir,
		ToolPolicy:     def.ToolPolicy,
		ParentRegistry: r.deps.ParentRegistry,
		WireEmitter:    newSubagentWireRelay(record.AgentID, r.deps.WireEmitter, outputWriter),
	}, contextDir)
	if err != nil {
		return soul.StepResult{}, outputWriter, err
	}
	if r.deps.ApprovalRuntime != nil {
		source := approvalruntime.ApprovalSource{
			Kind:         approvalruntime.SourceForegroundTurn,
			ID:           strings.TrimSpace(record.AgentID),
			AgentID:      strings.TrimSpace(record.AgentID),
			SubagentType: strings.TrimSpace(record.SubagentType),
		}
		if req.Background {
			source.Kind = approvalruntime.SourceBackgroundAgent
		}
		engine.SetApprovalRuntime(r.deps.ApprovalRuntime, source)
	}

	result, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: req.Prompt},
	})
	if err != nil {
		outputWriter.RecordError(err)
		return soul.StepResult{}, outputWriter, err
	}
	result = r.maybeContinueSummary(ctx, engine, result, outputWriter)

	if req.ModelOverride != "" {
		record.LaunchSpec.ModelOverride = req.ModelOverride
		record.LaunchSpec.EffectiveModel = resolveEffectiveModel(req.ModelOverride, def.DefaultModel, r.deps.Provider.ModelName())
	}
	return result, outputWriter, nil
}

func (r *ForegroundSubagentRunner) agentContextDir(agentID string) (string, error) {
	baseDir, err := r.deps.Store.storeDir()
	if err != nil {
		return "", err
	}
	normalizedAgentID, err := normalizeAgentID(agentID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, normalizedAgentID), nil
}

func buildRunReturnValue(record *AgentInstanceRecord, result soul.StepResult, outputWriter *SubagentOutputWriter) types.ToolReturnValue {
	outputText := contentPartsText(result.Content)
	transcript := outputWriter.Snapshot()
	return types.ToolReturnValue{
		Value: map[string]any{
			"agent_id":      record.AgentID,
			"subagent_type": record.SubagentType,
			"status":        string(record.Status),
			"output_text":   outputText,
			"content":       result.Content,
			"tool_calls":    result.ToolCalls,
			"tool_results":  result.ToolResults,
			"transcript":    transcript,
			"usage": map[string]any{
				"input_tokens":  result.Usage.InputTokens,
				"output_tokens": result.Usage.OutputTokens,
				"total_tokens":  result.Usage.TotalTokens,
			},
		},
	}
}

func contentPartsText(parts types.ContentParts) string {
	if len(parts) == 0 {
		return ""
	}

	var builder strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			builder.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				builder.WriteString(typed.Text)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func (r *ForegroundSubagentRunner) maybeContinueSummary(
	ctx context.Context,
	engine *soul.Soul,
	result soul.StepResult,
	outputWriter *SubagentOutputWriter,
) soul.StepResult {
	if r == nil || engine == nil {
		return result
	}
	minChars := r.deps.SummaryContinuationMinChars
	if minChars <= 0 {
		return result
	}
	if utf8.RuneCountInString(strings.TrimSpace(contentPartsText(result.Content))) >= minChars {
		return result
	}

	originalRegistry := engine.ToolRegistry()
	engine.SetToolRegistry(nil)
	defer engine.SetToolRegistry(originalRegistry)

	continuation, err := engine.Run(ctx, types.ContentParts{
		types.TextPart{Text: defaultSummaryContinuationPrompt},
	})
	if err != nil {
		if outputWriter != nil {
			outputWriter.RecordError(fmt.Sprintf("summary continuation: %v", err))
		}
		return result
	}
	return mergeStepResult(result, continuation)
}

func mergeStepResult(base soul.StepResult, continuation soul.StepResult) soul.StepResult {
	merged := base
	if len(continuation.Content) > 0 {
		merged.Content = continuation.Content
	}
	if len(continuation.ToolCalls) > 0 {
		merged.ToolCalls = append(append([]types.ToolCall(nil), base.ToolCalls...), continuation.ToolCalls...)
	}
	if len(continuation.ToolResults) > 0 {
		merged.ToolResults = append(append([]types.ToolResult(nil), base.ToolResults...), continuation.ToolResults...)
	}
	merged.Usage = types.TokenUsage{
		InputTokens:  base.Usage.InputTokens + continuation.Usage.InputTokens,
		OutputTokens: base.Usage.OutputTokens + continuation.Usage.OutputTokens,
		TotalTokens:  base.Usage.TotalTokens + continuation.Usage.TotalTokens,
	}
	return merged
}

func resolveEffectiveModel(modelOverride, defaultModel, providerModel string) string {
	if model := strings.TrimSpace(modelOverride); model != "" {
		return model
	}
	if model := strings.TrimSpace(defaultModel); model != "" {
		return model
	}
	return strings.TrimSpace(providerModel)
}

func runningStatusFor(background bool) SubagentStatus {
	if background {
		return StatusRunningBackground
	}
	return StatusRunningForeground
}

func nowUnixSeconds() float64 {
	return float64(time.Now().UTC().UnixNano()) / 1e9
}

func newForegroundAgentID() string {
	sequence := atomic.AddUint64(&foregroundAgentSequence, 1)
	return fmt.Sprintf("agent-%d-%d", time.Now().UTC().UnixNano(), sequence)
}
