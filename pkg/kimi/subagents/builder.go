package subagents

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

// BuildConfig defines runtime dependencies used to build one subagent soul instance.
type BuildConfig struct {
	Provider       llm.ChatProvider
	SystemPrompt   string
	WorkDir        string
	ToolPolicy     ToolPolicy
	ParentRegistry soul.ToolRegistry
}

// Build constructs one subagent soul runtime from the type definition and runtime config.
func Build(def *AgentTypeDefinition, cfg BuildConfig, contextDir string) (*soul.Soul, error) {
	definition, err := normalizeTypeDefinition(def)
	if err != nil {
		return nil, err
	}
	if cfg.Provider == nil {
		return nil, errors.New("subagents: nil provider")
	}

	resolvedContextDir, err := resolveContextDir(cfg.WorkDir, contextDir)
	if err != nil {
		return nil, err
	}

	policy := resolveToolPolicy(definition.ToolPolicy, cfg.ToolPolicy)
	registry, err := buildPolicyRegistry(policy, cfg.ParentRegistry)
	if err != nil {
		return nil, err
	}

	ctxStore := soul.NewSoulContext(resolvedContextDir)
	if err := ctxStore.Restore(); err != nil {
		return nil, fmt.Errorf("subagents: restore context: %w", err)
	}

	emitter := wireFileEmitter{
		file: wire.NewWireFile(filepath.Join(resolvedContextDir, wireFileName)),
	}

	return soul.NewSoul(
		cfg.Provider,
		ctxStore,
		registry,
		emitter,
		strings.TrimSpace(cfg.SystemPrompt),
	), nil
}

func normalizeTypeDefinition(def *AgentTypeDefinition) (*AgentTypeDefinition, error) {
	normalized, ok := cloneTypeDefinition(def)
	if !ok {
		return nil, errors.New("subagents: invalid type definition")
	}
	return normalized, nil
}

func resolveContextDir(workDir, contextDir string) (string, error) {
	resolved := strings.TrimSpace(contextDir)
	if resolved == "" {
		return "", errors.New("subagents: context dir is empty")
	}
	if !filepath.IsAbs(resolved) {
		base := strings.TrimSpace(workDir)
		if base != "" {
			resolved = filepath.Join(filepath.Clean(base), resolved)
		}
	}
	return filepath.Clean(resolved), nil
}

func resolveToolPolicy(fromDefinition, fromConfig ToolPolicy) ToolPolicy {
	policy := fromDefinition
	if fromConfig.Mode != "" || len(fromConfig.Allowlist) > 0 {
		policy = fromConfig
	}

	policy.Mode = ToolPolicyMode(strings.TrimSpace(string(policy.Mode)))
	if policy.Mode == "" {
		policy.Mode = ToolPolicyInherit
	}

	if len(policy.Allowlist) > 0 {
		normalized := make([]string, 0, len(policy.Allowlist))
		seen := map[string]struct{}{}
		for i := range policy.Allowlist {
			name := strings.TrimSpace(policy.Allowlist[i])
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			normalized = append(normalized, name)
		}
		policy.Allowlist = normalized
	}

	return policy
}

func buildPolicyRegistry(policy ToolPolicy, parent soul.ToolRegistry) (soul.ToolRegistry, error) {
	if parent == nil {
		return nil, nil
	}

	switch policy.Mode {
	case ToolPolicyInherit:
		return clonePolicyRegistry(parent, nil), nil
	case ToolPolicyAllowlist:
		allowSet := make(map[string]struct{}, len(policy.Allowlist))
		for i := range policy.Allowlist {
			allowSet[policy.Allowlist[i]] = struct{}{}
		}
		return clonePolicyRegistry(parent, allowSet), nil
	default:
		return nil, fmt.Errorf("subagents: unsupported tool policy mode %q", policy.Mode)
	}
}

func clonePolicyRegistry(parent soul.ToolRegistry, allowSet map[string]struct{}) soul.ToolRegistry {
	if parent == nil {
		return nil
	}

	definitions := parent.Definitions()
	if len(definitions) == 0 {
		return nil
	}

	filteredDefs := make([]llm.ToolDefinition, 0, len(definitions))
	executors := make(map[string]soul.ToolExecutor, len(definitions))
	for i := range definitions {
		name := strings.TrimSpace(definitions[i].Name)
		if name == "" {
			continue
		}
		if allowSet != nil {
			if _, ok := allowSet[name]; !ok {
				continue
			}
		}

		copied := definitions[i]
		copied.Name = name
		copied.Description = strings.TrimSpace(copied.Description)
		filteredDefs = append(filteredDefs, copied)

		if executor, ok := parent.Executor(name); ok && executor != nil {
			executors[name] = executor
		}
	}

	if len(filteredDefs) == 0 && len(executors) == 0 {
		return nil
	}

	sort.Slice(filteredDefs, func(i, j int) bool {
		return filteredDefs[i].Name < filteredDefs[j].Name
	})

	return &staticToolRegistry{
		definitions: filteredDefs,
		executors:   executors,
	}
}

type staticToolRegistry struct {
	definitions []llm.ToolDefinition
	executors   map[string]soul.ToolExecutor
}

func (r *staticToolRegistry) Definitions() []llm.ToolDefinition {
	if r == nil || len(r.definitions) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(r.definitions))
	copy(out, r.definitions)
	return out
}

func (r *staticToolRegistry) Executor(name string) (soul.ToolExecutor, bool) {
	if r == nil || r.executors == nil {
		return nil, false
	}
	executor, ok := r.executors[strings.TrimSpace(name)]
	if !ok || executor == nil {
		return nil, false
	}
	return executor, true
}

type wireFileEmitter struct {
	file *wire.WireFile
}

func (e wireFileEmitter) Emit(msg wire.WireMessage) error {
	if e.file == nil {
		return errors.New("subagents: nil wire file")
	}
	return e.file.AppendMessage(msg)
}
