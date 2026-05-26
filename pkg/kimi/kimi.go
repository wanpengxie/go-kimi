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

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/agentspec"
	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
	corebg "github.com/wanpengxie/go-kimi/pkg/kimi/background"
	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/mcp"
	"github.com/wanpengxie/go-kimi/pkg/kimi/session"
	"github.com/wanpengxie/go-kimi/pkg/kimi/skill"
	"github.com/wanpengxie/go-kimi/pkg/kimi/subagents"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	agenttool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/agent"
	bgtools "github.com/wanpengxie/go-kimi/pkg/kimi/tools/background"
	dmailtool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/dmail"
	toolfile "github.com/wanpengxie/go-kimi/pkg/kimi/tools/file"
	plantool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/plan"
	questiontool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/question"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/shell"
	sbtools "github.com/wanpengxie/go-kimi/pkg/kimi/tools/sandbox"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/think"
	webtool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/web"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

const (
	// Version is the SDK placeholder version.
	Version = "v1.0.0"

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

	// SandboxBackend optionally redirects the standard tool set
	// (shell / read_file / write_file / str_replace / grep / glob /
	// read_media_file) to a custom execution layer.
	//
	// When nil, NewAgent registers the in-process tools from tools/shell +
	// tools/file as before — this is the historical behaviour and is the
	// default for direct CLI / library users.
	//
	// When non-nil, NewAgent installs sandbox.StandardToolsFromBackend(...)
	// in place of the in-process tools and routes every standard tool call
	// through the supplied backend. The non-sandbox tools (think / question /
	// plan / agent / background / dmail / web) keep their existing
	// in-process implementations unchanged.
	//
	// This is the seam used by cloudagent and any other adopter that needs
	// to forward execution somewhere else (a docker container, a remote
	// machine, an arbitrary sandbox) — see pkg/kimi/tools/sandbox for the
	// background and the LocalBackend reference implementation.
	SandboxBackend sbtools.SandboxBackend

	// DisableStandardSandboxTools, when true, suppresses the standard
	// sandbox tool set entirely (shell / read_file / write_file /
	// str_replace / grep / glob / read_media_file). The non-sandbox tools
	// (think / question / plan / agent / background / dmail / web) are
	// still registered.
	//
	// This is for adopters that ship their own catalog of "shell"-class
	// tools via AdditionalTools and need to be the sole declarer of those
	// names. Without this flag, the standard sandbox tools win the
	// candidate-dedup race against same-named AdditionalTools (first
	// candidate registered for a given name wins — see filterTools), so
	// the AdditionalTools entry is silently dropped.
	//
	// cloudagent uses this: the brain↔hand wire ships the hand's own
	// manifest (which already includes shell / read_file / etc., with the
	// hand's own input schemas), and the brain MUST forward every tool
	// call to the hand rather than execute anything in-process. Setting
	// this flag (alongside AdditionalTools containing the manifest tools)
	// gives the LLM exactly the hand-declared catalog with no name clash.
	//
	// When SandboxBackend is non-nil, this flag is ignored —
	// SandboxBackend already redirects the standard tools (it does not
	// suppress them), which is the documented behaviour for that path.
	DisableStandardSandboxTools bool

	// SkillRoots optionally overrides the directories that NewAgent scans
	// for SKILL.md definitions.
	//
	// Semantics:
	//   nil           → fall back to skill.DefaultSkillRoots(workDir)
	//                   (builtin/skills + $HOME/.kimi/skills + workDir/.kimi/skills).
	//   non-nil       → use the slice as-is, in ascending priority order
	//                   (later roots override earlier roots on name conflict).
	//                   Even an empty non-nil slice disables default
	//                   discovery entirely — this lets adopters run
	//                   hermetic skill discovery without picking up
	//                   arbitrary files from the user's home directory.
	//
	// Note: a SKILL.md that fails to parse is logged as a warning and
	// skipped; it never aborts NewAgent.
	SkillRoots []string
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
		wireHub.Close()
		_ = wireRecorder.Close()
		wireMerger.Close()
	}
	planSyncer := newPlanModeSyncer(sess, planMode)

	market := newLaborMarket(resolvedSpec, effectiveModel)
	subagentStore := subagents.NewSubagentStore(sess.SubagentsDir())
	foregroundRunner := subagents.NewForegroundSubagentRunner(subagents.RunnerDeps{
		Market:                      market,
		Store:                       subagentStore,
		Provider:                    provider,
		ParentRegistry:              toolRegistry,
		SystemPrompt:                resolvedSpec.SystemPrompt,
		WorkDir:                     workDir,
		WireEmitter:                 wireEmitter,
		SummaryContinuationMinChars: 200,
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
		cfg.SandboxBackend,
		cfg.DisableStandardSandboxTools,
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

	rootSkills, parseErrs, err := skill.DiscoverFromRoots(resolveSkillRoots(cfg, workDir))
	if err != nil {
		if mcpLoader != nil {
			_ = mcpLoader.Close()
		}
		cleanupWire()
		return nil, fmt.Errorf("kimi: discover skills: %w", err)
	}
	for _, pe := range parseErrs {
		// Single-file parse failures are reported but do not abort
		// agent boot — DiscoverFromRoots already filtered them out of
		// the returned skill map. Surface as a warning so deployments
		// keep working even when a user-installed SKILL.md is malformed.
		fmt.Fprintf(os.Stderr, "kimi: skip skill %q: %v\n", pe.Path, pe.Err)
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
		if a.wireHub != nil {
			a.wireHub.Close()
		}
		if a.wireRecorder != nil {
			if err := a.wireRecorder.Close(); err != nil {
				errs = append(errs, fmt.Errorf("kimi: close wire recorder: %w", err))
			}
		}
		if a.wireMerger != nil {
			a.wireMerger.Close()
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

// RegisteredToolNames returns the sorted set of tool names that the
// agent's LLM-facing catalog currently exposes. It is a read-only
// introspection helper — useful for adopters (cloudagent, tests) that
// need to assert which tools the agent will actually offer the model
// without spinning a real LLM round-trip.
//
// Returns nil when the agent or its engine is uninitialised.
func (a *Agent) RegisteredToolNames() []string {
	if a == nil || a.engine == nil {
		return nil
	}
	registry := a.engine.ToolRegistry()
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	if len(defs) == 0 {
		return nil
	}
	names := make([]string, 0, len(defs))
	for i := range defs {
		names = append(names, defs[i].Name)
	}
	return names
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
	provider := agentCfg.Provider
	if provider != nil {
		// Caller-supplied providers are treated as fully configured and must not be rewritten.
		return provider, strings.TrimSpace(provider.ModelName()), nil
	}

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
	sandboxBackend sbtools.SandboxBackend,
	disableStandardSandbox bool,
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

	// Sandbox-related tools (shell + file family) are routed through the
	// SandboxBackend seam when provided; otherwise we keep the historical
	// in-process implementations so existing callers see no behaviour
	// change. The agent's tool catalog stays identical either way — it is
	// only the execution layer that flips.
	//
	// When disableStandardSandbox is true (and SandboxBackend is nil),
	// the standard sandbox tools are suppressed entirely. The caller is
	// expected to ship its own catalog of these names via AdditionalTools.
	// See AgentConfig.DisableStandardSandboxTools for the rationale.
	var sandboxTools []tools.Tool
	switch {
	case sandboxBackend != nil:
		sandboxTools = sbtools.StandardToolsFromBackend(sandboxBackend)
	case disableStandardSandbox:
		sandboxTools = nil
	default:
		sandboxTools = []tools.Tool{
			shell.NewWithBackground(workDir, nil, backgroundManager, sessionID),
			toolfile.NewReadFile(workDir),
			toolfile.NewReadMediaFile(workDir, supportsVision, supportsVideo),
			toolfile.NewWriteFile(workDir, nil),
			toolfile.NewStrReplace(workDir, nil),
			toolfile.NewGrep(workDir),
			toolfile.NewGlob(workDir),
		}
	}

	// Non-sandbox tools (agent meta-controls + IO tools that aren't bound
	// to the sandbox concept) keep their in-process implementations
	// regardless of backend choice.
	candidates := []tools.Tool{think.New()}
	candidates = append(candidates, sandboxTools...)
	candidates = append(candidates,
		questiontool.New(questionHub, questionPublisher, yoloChecker),
		enterPlan,
		exitPlan,
		dmailtool.New(dmailContext),
		agenttool.New(foregroundRunner, backgroundManager),
		bgtools.NewTaskList(backgroundManager),
		bgtools.NewTaskOutput(backgroundManager),
		bgtools.NewTaskStop(backgroundManager),
	)

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
			Name:           strings.TrimSpace(client.Name),
			Transport:      mcp.TransportStdio,
			TimeoutSeconds: client.TimeoutSeconds,
			Command:        strings.TrimSpace(client.Command),
			Args:           append([]string(nil), client.Args...),
			Env:            cloneStringMap(client.Env),
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

// resolveSkillRoots returns the skill-discovery roots NewAgent should scan.
//
// A nil cfg.SkillRoots means "use the historical default" (built-in / user /
// project). A non-nil slice — including a non-nil empty slice — is honoured
// verbatim, which lets callers run hermetic skill discovery that ignores
// $HOME/.kimi/skills and similar stray directories.
func resolveSkillRoots(cfg AgentConfig, workDir string) []string {
	if cfg.SkillRoots == nil {
		return skill.DefaultSkillRoots(workDir)
	}
	return cfg.SkillRoots
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
