// Package kimi exposes the public SDK surface for go-kimi.
package kimi

import (
	"context"
	stdErrors "errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/agentspec"
	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	kimierrors "github.com/xiewanpeng/go-kimi/pkg/kimi/errors"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/mcp"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/session"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/skill"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/subagents"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	agenttool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/agent"
	bgtools "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/background"
	dmailtool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/dmail"
	toolfile "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/file"
	plantool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/plan"
	questiontool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/question"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/shell"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/think"
	webtool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/web"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	// Version is the SDK placeholder version.
	Version = "0.0.0-dev"

	defaultSubagentType = "general-purpose"
	closeTimeout        = 3 * time.Second
)

// AgentOverrides stores one-off runtime overrides applied on top of config/spec.
type AgentOverrides struct {
	SystemPrompt    string
	Model           string
	AllowedTools    []string
	ExcludedTools   []string
	DefaultThinking string
	DefaultYolo     *bool
}

// AgentConfig combines config + spec + runtime overrides for NewAgent.
type AgentConfig struct {
	WorkDir   string
	SessionID string

	ConfigPath string
	Config     config.Config

	SpecPath string
	Spec     *agentspec.ResolvedSpec

	Overrides AgentOverrides

	Provider        llm.ChatProvider
	WireEmitter     wire.Emitter
	AdditionalTools []tools.Tool

	ApprovalRuntime *approvalruntime.ApprovalRuntime
}

// Agent is one assembled SDK runtime facade.
type Agent struct {
	engine            *soul.Soul
	session           *session.Session
	approvalRuntime   *approvalruntime.ApprovalRuntime
	backgroundManager *corebg.BackgroundTaskManager
	mcpLoader         *mcp.MCPToolLoader

	provider llm.ChatProvider

	wireHub      *wire.Hub
	wireMerger   *wire.MergingSubscriber
	wireRecorder *wire.Recorder

	mu         sync.RWMutex
	lastResult soul.StepResult

	closeOnce sync.Once
	closeErr  error
}

// NewAgent assembles config -> provider -> tools -> skill -> soul -> wire -> approval.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	workDir, err := resolveWorkDir(cfg.WorkDir)
	if err != nil {
		return nil, err
	}

	runtimeConfig, err := resolveRuntimeConfig(cfg)
	if err != nil {
		return nil, err
	}

	resolvedSpec, err := resolveRuntimeSpec(cfg)
	if err != nil {
		return nil, err
	}
	applyOverridesToSpec(resolvedSpec, cfg.Overrides)

	sess, err := openSession(workDir, cfg.SessionID)
	if err != nil {
		return nil, err
	}

	ctxStore := soul.NewSoulContext(sess.Dir)
	if err := ctxStore.Restore(); err != nil {
		return nil, fmt.Errorf("kimi: restore session context: %w", err)
	}

	provider, effectiveModel, err := resolveProvider(runtimeConfig, resolvedSpec, cfg)
	if err != nil {
		return nil, err
	}
	yolo := runtimeConfig.DefaultYolo
	if cfg.Overrides.DefaultYolo != nil {
		yolo = *cfg.Overrides.DefaultYolo
	}

	planState := plantool.NewPlanState()
	planFile := ""
	planMode := soul.PlanModeState{}
	if sess.State != nil {
		sess.State.Yolo = yolo

		planSlug := strings.TrimSpace(sess.State.PlanSlug)
		planFile = planFilePath(workDir, planSlug)
		if sess.State.PlanMode && planFile != "" {
			_ = planState.Enter(planFile)
		}
		planMode = soul.PlanModeState{
			Active:    planState.IsActive(),
			SessionID: strings.TrimSpace(sess.State.PlanSessionID),
			Slug:      planSlug,
			PlanFile:  planFile,
		}
		if planMode.SessionID == "" {
			planMode.SessionID = strings.TrimSpace(sess.ID)
		}
		sess.State.PlanMode = planMode.Active
		sess.State.PlanSessionID = planMode.SessionID
		sess.State.PlanSlug = planMode.Slug
		if err := sess.SaveState(); err != nil {
			return nil, fmt.Errorf("kimi: persist session state: %w", err)
		}
	}

	modelCapabilities := resolveModelCapabilities(runtimeConfig, effectiveModel, provider)
	supportsVision := modelCapabilities[types.ModelCapabilityVision]
	supportsVideo := modelCapabilities[types.ModelCapabilityVideoInput]

	toolRegistry := tools.NewMapToolRegistry()

	wireHub := wire.NewHub(64)
	wireMerger := wire.NewMergingSubscriber(wireHub, 128)
	wireRecorder := wire.NewRecorder(wire.NewWireFile(sess.WireFile), wireMerger.Messages())
	wireEmitter := composeEmitters(cfg.WireEmitter, wireHub)
	cleanupWire := func() {
		wireMerger.Close()
		wireHub.Close()
		_ = wireRecorder.Close()
	}
	planSyncer := newPlanModeSyncer(sess, planMode)

	market := newLaborMarket(resolvedSpec, effectiveModel)
	subagentStore := subagents.NewSubagentStore(sess.SubagentsDir())
	foregroundRunner := subagents.NewForegroundSubagentRunner(subagents.RunnerDeps{
		Market:         market,
		Store:          subagentStore,
		Provider:       provider,
		ParentRegistry: toolRegistry,
		SystemPrompt:   resolvedSpec.SystemPrompt,
		WorkDir:        workDir,
	})

	backgroundStore := corebg.NewBackgroundTaskStore(sess.TasksDir())
	backgroundManager := corebg.NewBackgroundTaskManager(corebg.ManagerDeps{
		Store:          backgroundStore,
		SubagentRunner: foregroundRunner,
	})

	mcpTools, mcpLoader, err := loadMCPTools(runtimeConfig)
	if err != nil {
		cleanupWire()
		return nil, err
	}

	candidates := buildToolCandidates(
		runtimeConfig,
		workDir,
		sess.ID,
		planState,
		foregroundRunner,
		backgroundManager,
		wireHub,
		wireEmitter,
		func() bool { return yolo },
		supportsVision,
		supportsVideo,
		ctxStore,
		planSyncer,
	)
	candidates = append(candidates, mcpTools...)
	candidates = append(candidates, cfg.AdditionalTools...)

	selectedTools, err := filterTools(candidates, resolvedSpec)
	if err != nil {
		if mcpLoader != nil {
			_ = mcpLoader.Close()
		}
		cleanupWire()
		return nil, err
	}
	for i := range selectedTools {
		toolRegistry.Register(selectedTools[i])
	}

	engine := soul.NewSoul(provider, ctxStore, toolRegistry, wireEmitter, resolvedSpec.SystemPrompt)
	engine.SetMaxSteps(runtimeConfig.Loop.MaxTurns)
	engine.SetStepRetryConfig(soul.StepRetryConfig{MaxRetries: runtimeConfig.Loop.MaxRetriesPerStep})

	engine.SetYolo(yolo)
	planSyncer.AttachEngine(engine)
	engine.SetPlanModeState(planMode)

	approval := cfg.ApprovalRuntime
	if approval == nil {
		approval = approvalruntime.NewApprovalRuntime()
	}
	engine.SetApprovalRuntime(approval, approvalruntime.ApprovalSource{
		Kind: approvalruntime.SourceForegroundTurn,
		ID:   strings.TrimSpace(sess.ID),
	})

	rootSkills, err := skill.DiscoverFromRoots(skill.DefaultSkillRoots(workDir))
	if err != nil {
		if mcpLoader != nil {
			_ = mcpLoader.Close()
		}
		cleanupWire()
		return nil, fmt.Errorf("kimi: discover skills: %w", err)
	}
	skill.RegisterSkills(engine, rootSkills)

	return &Agent{
		engine:            engine,
		session:           sess,
		approvalRuntime:   approval,
		backgroundManager: backgroundManager,
		mcpLoader:         mcpLoader,
		provider:          provider,
		wireHub:           wireHub,
		wireMerger:        wireMerger,
		wireRecorder:      wireRecorder,
	}, nil
}

// Run executes one foreground turn with plain-text user input.
func (a *Agent) Run(ctx context.Context, input string) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return &kimierrors.ConfigError{
			Field:  "run.input",
			Reason: "must not be empty",
			Cause:  kimierrors.ErrConfigInvalid,
		}
	}
	return a.runParts(ctx, types.ContentParts{types.TextPart{Text: input}})
}

func (a *Agent) runParts(ctx context.Context, input types.ContentParts) error {
	if a == nil || a.engine == nil {
		return stdErrors.Join(kimierrors.ErrLLMNotConfigured, stdErrors.New("kimi: nil agent engine"))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	result, err := a.engine.Run(ctx, input)
	if err != nil {
		return classifyRunError(err, a.provider)
	}

	a.mu.Lock()
	a.lastResult = result
	a.mu.Unlock()
	return nil
}

// Steer injects extra user input into a currently running turn.
func (a *Agent) Steer(ctx context.Context, input string) error {
	if a == nil || a.engine == nil {
		return stdErrors.Join(kimierrors.ErrLLMNotConfigured, stdErrors.New("kimi: nil agent engine"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.engine.Steer(ctx, input); err != nil {
		if stdErrors.Is(err, context.Canceled) || stdErrors.Is(err, context.DeadlineExceeded) {
			return stdErrors.Join(kimierrors.ErrRunCancelled, err)
		}
		return err
	}
	return nil
}

// Compact triggers one manual context compaction.
func (a *Agent) Compact(ctx context.Context) error {
	if a == nil || a.engine == nil {
		return stdErrors.Join(kimierrors.ErrLLMNotConfigured, stdErrors.New("kimi: nil agent engine"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return a.engine.Compact(ctx)
}

// Close releases managed runtime resources.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}

	a.closeOnce.Do(func() {
		var errs []error

		if a.backgroundManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			if err := a.backgroundManager.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
			cancel()
		}
		if a.mcpLoader != nil {
			if err := a.mcpLoader.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if a.wireMerger != nil {
			a.wireMerger.Close()
		}
		if a.wireHub != nil {
			a.wireHub.Close()
		}
		if a.wireRecorder != nil {
			if err := a.wireRecorder.Close(); err != nil {
				errs = append(errs, fmt.Errorf("kimi: close wire recorder: %w", err))
			}
		}
		if a.session != nil {
			if err := a.session.SaveState(); err != nil {
				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			a.closeErr = stdErrors.Join(errs...)
		}
	})
	return a.closeErr
}

// LastResult returns one snapshot of the latest successful Run result.
func (a *Agent) LastResult() soul.StepResult {
	if a == nil {
		return soul.StepResult{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastResult
}

func resolveRuntimeConfig(cfg AgentConfig) (config.Config, error) {
	if path := strings.TrimSpace(cfg.ConfigPath); path != "" {
		loaded, err := config.LoadConfigWithEnv(path)
		if err != nil {
			return config.Config{}, &kimierrors.ConfigError{
				Field:  "config_path",
				Reason: "load failed",
				Cause:  err,
			}
		}
		return loaded, nil
	}

	runtimeConfig := cfg.Config
	if reflect.DeepEqual(runtimeConfig, config.Config{}) {
		runtimeConfig = config.NewDefaultConfig()
	}
	if err := runtimeConfig.Validate(); err != nil {
		return config.Config{}, &kimierrors.ConfigError{
			Field:  "config",
			Reason: "validation failed",
			Cause:  err,
		}
	}
	return runtimeConfig, nil
}

func resolveRuntimeSpec(cfg AgentConfig) (*agentspec.ResolvedSpec, error) {
	if cfg.Spec != nil {
		return cloneResolvedSpec(cfg.Spec), nil
	}
	if path := strings.TrimSpace(cfg.SpecPath); path != "" {
		spec, err := agentspec.ResolveAgentSpec(path)
		if err != nil {
			return nil, &kimierrors.ConfigError{
				Field:  "spec_path",
				Reason: "resolve failed",
				Cause:  err,
			}
		}
		return spec, nil
	}
	return &agentspec.ResolvedSpec{Name: "default-agent"}, nil
}

func applyOverridesToSpec(spec *agentspec.ResolvedSpec, overrides AgentOverrides) {
	if spec == nil {
		return
	}
	if prompt := strings.TrimSpace(overrides.SystemPrompt); prompt != "" {
		spec.SystemPrompt = prompt
	}
	if model := strings.TrimSpace(overrides.Model); model != "" {
		spec.Model = model
	}
	spec.AllowedTools = mergeUniqueStrings(spec.AllowedTools, overrides.AllowedTools)
	spec.ExcludedTools = mergeUniqueStrings(spec.ExcludedTools, overrides.ExcludedTools)
}

func resolveProvider(cfg config.Config, spec *agentspec.ResolvedSpec, agentCfg AgentConfig) (llm.ChatProvider, string, error) {
	modelName := strings.TrimSpace(spec.Model)
	if override := strings.TrimSpace(agentCfg.Overrides.Model); override != "" {
		modelName = override
	}
	if modelName == "" {
		modelName = strings.TrimSpace(cfg.DefaultModel)
	}

	providerName := strings.TrimSpace(cfg.DefaultProvider)
	if modelName != "" {
		model, ok := findModel(cfg.Models, modelName)
		if !ok {
			return nil, "", stdErrors.Join(
				kimierrors.ErrModelNotFound,
				fmt.Errorf("kimi: model %q not found", modelName),
			)
		}
		providerName = strings.TrimSpace(model.Provider)
	}

	providerModel, ok := findProvider(cfg.Providers, providerName)
	if !ok {
		return nil, "", stdErrors.Join(
			kimierrors.ErrProviderNotFound,
			fmt.Errorf("kimi: provider %q not found", providerName),
		)
	}

	provider := agentCfg.Provider
	if provider == nil {
		constructed, err := llm.NewProvider(llm.ProviderConfig{
			Type:    providerModel.Type,
			BaseURL: providerModel.BaseURL,
			APIKey:  providerModel.APIKey.Raw(),
			Model:   modelName,
		})
		if err != nil {
			return nil, "", &kimierrors.LLMError{
				Provider: providerModel.Name,
				Cause:    err,
			}
		}
		provider = constructed
	}

	if modelName != "" {
		provider = provider.WithModel(modelName)
	}

	thinking := strings.TrimSpace(cfg.DefaultThinking)
	if overrideThinking := strings.TrimSpace(agentCfg.Overrides.DefaultThinking); overrideThinking != "" {
		thinking = overrideThinking
	}
	provider = llm.WithThinking(provider, llm.ThinkingEffort(thinking))
	return provider, modelName, nil
}

func buildToolCandidates(
	cfg config.Config,
	workDir string,
	sessionID string,
	planState *plantool.PlanState,
	foregroundRunner *subagents.ForegroundSubagentRunner,
	backgroundManager *corebg.BackgroundTaskManager,
	questionHub *wire.Hub,
	questionPublisher wire.Emitter,
	yoloChecker questiontool.YoloChecker,
	supportsVision bool,
	supportsVideo bool,
	dmailContext dmailtool.MailContext,
	planSyncer plantool.ModeSyncer,
) []tools.Tool {
	enterPlan := plantool.NewEnterPlanMode(workDir, planState).
		ConfigureQuestionFlow(plantool.QuestionFlow{
			Hub:       questionHub,
			Publisher: questionPublisher,
			IsYolo:    plantool.YoloChecker(yoloChecker),
		}).
		SetModeSyncer(planSyncer)
	exitPlan := plantool.NewExitPlanMode(planState).
		ConfigureQuestionFlow(plantool.QuestionFlow{
			Hub:       questionHub,
			Publisher: questionPublisher,
			IsYolo:    plantool.YoloChecker(yoloChecker),
		}).
		SetModeSyncer(planSyncer)

	candidates := []tools.Tool{
		think.New(),
		shell.NewWithBackground(workDir, nil, backgroundManager, sessionID),
		toolfile.NewReadFile(workDir),
		toolfile.NewReadMediaFile(workDir, supportsVision, supportsVideo),
		toolfile.NewWriteFile(workDir, nil),
		toolfile.NewStrReplace(workDir, nil),
		toolfile.NewGrep(workDir),
		toolfile.NewGlob(workDir),
		questiontool.New(questionHub, questionPublisher, yoloChecker),
		enterPlan,
		exitPlan,
		dmailtool.New(dmailContext),
		agenttool.New(foregroundRunner, backgroundManager),
		bgtools.NewTaskList(backgroundManager),
		bgtools.NewTaskOutput(backgroundManager),
		bgtools.NewTaskStop(backgroundManager),
	}

	if cfg.Services.MoonshotFetch.Enabled {
		fetchTool := webtool.NewFetchURL(http.DefaultClient)
		fetchTool.Timeout = time.Duration(cfg.Services.MoonshotFetch.TimeoutSeconds) * time.Second
		fetchTool.MaxContentBytes = cfg.Services.MoonshotFetch.MaxContentBytes
		candidates = append(candidates, fetchTool)
	}

	if cfg.Services.MoonshotSearch.Enabled {
		searchTool := webtool.NewSearchWeb(cfg.Services.MoonshotSearch.Endpoint, http.DefaultClient)
		searchTool.Timeout = time.Duration(cfg.Services.MoonshotSearch.TimeoutSeconds) * time.Second
		candidates = append(candidates, searchTool)
	}

	return candidates
}

func filterTools(candidates []tools.Tool, spec *agentspec.ResolvedSpec) ([]tools.Tool, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	available := make(map[string]tools.Tool, len(candidates))
	orderedNames := make([]string, 0, len(candidates))
	for i := range candidates {
		tool := candidates[i]
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		if _, exists := available[name]; exists {
			continue
		}
		available[name] = tool
		orderedNames = append(orderedNames, name)
	}

	allowedSet := map[string]struct{}{}
	excludedSet := map[string]struct{}{}
	if spec != nil {
		for i := range spec.AllowedTools {
			name := strings.TrimSpace(spec.AllowedTools[i])
			if name == "" {
				continue
			}
			if _, ok := available[name]; !ok {
				return nil, &kimierrors.ToolError{Name: name, Cause: kimierrors.ErrToolNotFound}
			}
			allowedSet[name] = struct{}{}
		}
		for i := range spec.ExcludedTools {
			name := strings.TrimSpace(spec.ExcludedTools[i])
			if name == "" {
				continue
			}
			excludedSet[name] = struct{}{}
		}
	}

	selected := make([]tools.Tool, 0, len(orderedNames))
	for i := range orderedNames {
		name := orderedNames[i]
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[name]; !ok {
				continue
			}
		}
		if _, excluded := excludedSet[name]; excluded {
			continue
		}
		selected = append(selected, available[name])
	}
	return selected, nil
}

func loadMCPTools(cfg config.Config) ([]tools.Tool, *mcp.MCPToolLoader, error) {
	if len(cfg.MCP.Clients) == 0 {
		return nil, nil, nil
	}

	serverConfigs := make([]mcp.MCPServerConfig, 0, len(cfg.MCP.Clients))
	for i := range cfg.MCP.Clients {
		client := cfg.MCP.Clients[i]
		if client.Disabled {
			continue
		}
		serverConfigs = append(serverConfigs, mcp.MCPServerConfig{
			Name:      strings.TrimSpace(client.Name),
			Transport: mcp.TransportStdio,
			Command:   strings.TrimSpace(client.Command),
			Args:      append([]string(nil), client.Args...),
			Env:       cloneStringMap(client.Env),
		})
	}
	if len(serverConfigs) == 0 {
		return nil, nil, nil
	}

	loader := mcp.NewMCPToolLoader(serverConfigs)
	discovered, err := loader.LoadAll(context.Background())
	if err != nil {
		_ = loader.Close()
		return nil, nil, fmt.Errorf("kimi: load mcp tools: %w", err)
	}
	return discovered, loader, nil
}

func newLaborMarket(spec *agentspec.ResolvedSpec, defaultModel string) *subagents.LaborMarket {
	market := subagents.NewLaborMarket()
	names := []string{defaultSubagentType}
	if spec != nil && len(spec.SubagentTypes) > 0 {
		names = append([]string(nil), spec.SubagentTypes...)
	}
	for i := range names {
		name := strings.TrimSpace(names[i])
		if name == "" {
			continue
		}
		market.Register(&subagents.AgentTypeDefinition{
			Name:               name,
			Description:        "SDK subagent",
			WhenToUse:          "Use when task decomposition is needed",
			DefaultModel:       defaultModel,
			ToolPolicy:         subagents.ToolPolicy{Mode: subagents.ToolPolicyInherit},
			SupportsBackground: true,
		})
	}
	return market
}

func resolveModelCapabilities(cfg config.Config, effectiveModel string, provider llm.ChatProvider) map[types.ModelCapability]bool {
	if model, ok := findModel(cfg.Models, effectiveModel); ok {
		return llm.DeriveModelCapabilities(model)
	}

	modelName := strings.TrimSpace(effectiveModel)
	if modelName == "" && provider != nil {
		modelName = strings.TrimSpace(provider.ModelName())
	}
	return llm.DeriveModelCapabilities(config.LLMModel{Name: modelName})
}

func planFilePath(workDir string, slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return filepath.Join(workDir, ".kimi", "plans", slug+".md")
}

type planModeSyncer struct {
	mu      sync.Mutex
	session *session.Session
	engine  *soul.Soul
	state   soul.PlanModeState
}

func newPlanModeSyncer(sess *session.Session, initial soul.PlanModeState) *planModeSyncer {
	return &planModeSyncer{
		session: sess,
		state:   initial,
	}
}

func (s *planModeSyncer) AttachEngine(engine *soul.Soul) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.engine = engine
	state := s.state
	s.mu.Unlock()
	if engine != nil {
		engine.SetPlanModeState(state)
	}
}

func (s *planModeSyncer) OnPlanModeEnter(planFile string, slug string) {
	if s == nil {
		return
	}
	planFile = strings.TrimSpace(planFile)
	slug = strings.TrimSpace(slug)
	if slug == "" && planFile != "" {
		base := filepath.Base(planFile)
		slug = strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	}

	s.mu.Lock()
	state := s.state
	state.Active = true
	state.PlanFile = planFile
	if slug != "" {
		state.Slug = slug
	}
	if state.SessionID == "" && s.session != nil {
		state.SessionID = strings.TrimSpace(s.session.ID)
	}
	s.state = state
	engine := s.engine
	sess := s.session
	s.mu.Unlock()

	applyPlanModeState(sess, engine, state)
}

func (s *planModeSyncer) OnPlanModeExit() {
	if s == nil {
		return
	}
	s.mu.Lock()
	state := s.state
	state.Active = false
	state.PlanFile = ""
	s.state = state
	engine := s.engine
	sess := s.session
	s.mu.Unlock()

	applyPlanModeState(sess, engine, state)
}

func applyPlanModeState(sess *session.Session, engine *soul.Soul, state soul.PlanModeState) {
	if sess != nil {
		if sess.State == nil {
			sess.State = session.NewSessionState()
		}
		sess.State.PlanMode = state.Active
		sess.State.PlanSessionID = strings.TrimSpace(state.SessionID)
		sess.State.PlanSlug = strings.TrimSpace(state.Slug)
		_ = sess.SaveState()
	}
	if engine != nil {
		engine.SetPlanModeState(state)
	}
}

func classifyRunError(err error, provider llm.ChatProvider) error {
	if err == nil {
		return nil
	}
	if stdErrors.Is(err, kimierrors.ErrMaxStepsReached) {
		return err
	}
	if stdErrors.Is(err, context.Canceled) || stdErrors.Is(err, context.DeadlineExceeded) {
		return stdErrors.Join(kimierrors.ErrRunCancelled, err)
	}
	if stdErrors.Is(err, kimierrors.ErrStepFailed) {
		providerName := ""
		if provider != nil {
			providerName = strings.TrimSpace(provider.ModelName())
		}
		return &kimierrors.LLMError{Provider: providerName, Cause: err}
	}
	return err
}

func openSession(workDir, sessionID string) (*session.Session, error) {
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		sess, err := session.Find(workDir, sessionID)
		if err != nil {
			return nil, fmt.Errorf("kimi: find session %q: %w", sessionID, err)
		}
		return sess, nil
	}
	sess, err := session.Create(workDir)
	if err != nil {
		return nil, fmt.Errorf("kimi: create session: %w", err)
	}
	return sess, nil
}

func resolveWorkDir(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("kimi: resolve workdir: %w", err)
		}
		workDir = cwd
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("kimi: resolve workdir %q: %w", workDir, err)
	}
	return filepath.Clean(abs), nil
}

func findProvider(providers []config.LLMProvider, name string) (config.LLMProvider, bool) {
	name = strings.TrimSpace(name)
	for i := range providers {
		if strings.TrimSpace(providers[i].Name) == name {
			return providers[i], true
		}
	}
	return config.LLMProvider{}, false
}

func findModel(models []config.LLMModel, name string) (config.LLMModel, bool) {
	name = strings.TrimSpace(name)
	for i := range models {
		if strings.TrimSpace(models[i].Name) == name {
			return models[i], true
		}
	}
	return config.LLMModel{}, false
}

func mergeUniqueStrings(base, overlays []string) []string {
	if len(base) == 0 && len(overlays) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(overlays))
	seen := map[string]struct{}{}
	for i := range base {
		name := strings.TrimSpace(base[i])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for i := range overlays {
		name := strings.TrimSpace(overlays[i])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneResolvedSpec(spec *agentspec.ResolvedSpec) *agentspec.ResolvedSpec {
	if spec == nil {
		return nil
	}
	cloned := *spec
	cloned.AllowedTools = append([]string(nil), spec.AllowedTools...)
	cloned.ExcludedTools = append([]string(nil), spec.ExcludedTools...)
	cloned.SubagentTypes = append([]string(nil), spec.SubagentTypes...)
	cloned.InheritanceChain = append([]string(nil), spec.InheritanceChain...)
	return &cloned
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

type wireFileEmitter struct {
	file *wire.WireFile
}

func (e wireFileEmitter) Emit(msg wire.WireMessage) error {
	if e.file == nil {
		return stdErrors.New("kimi: nil wire file")
	}
	return e.file.AppendMessage(msg)
}

type compositeEmitter struct {
	emitters []wire.Emitter
}

func (e compositeEmitter) Emit(msg wire.WireMessage) error {
	var errs []error
	for i := range e.emitters {
		emitter := e.emitters[i]
		if emitter == nil {
			continue
		}
		if err := emitter.Emit(msg); err != nil {
			errs = append(errs, err)
		}
	}
	return stdErrors.Join(errs...)
}

func composeEmitters(primary wire.Emitter, fallback wire.Emitter) wire.Emitter {
	emitters := make([]wire.Emitter, 0, 2)
	if fallback != nil {
		emitters = append(emitters, fallback)
	}
	if primary != nil {
		emitters = append(emitters, primary)
	}
	if len(emitters) == 0 {
		return wire.NoopEmitter{}
	}
	if len(emitters) == 1 {
		return emitters[0]
	}
	return compositeEmitter{emitters: emitters}
}
