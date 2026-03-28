package agentspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolPolicy defines declarative tool allow/exclude rules.
type ToolPolicy struct {
	AllowedTools  []string `yaml:"allowed_tools,omitempty" json:"allowed_tools,omitempty"`
	ExcludedTools []string `yaml:"excluded_tools,omitempty" json:"excluded_tools,omitempty"`

	Allow   []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// AgentSpec is one declarative agent spec loaded from YAML.
type AgentSpec struct {
	Name          string     `yaml:"name" json:"name"`
	Extends       string     `yaml:"extends,omitempty" json:"extends,omitempty"`
	SystemPrompt  string     `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	Model         string     `yaml:"model,omitempty" json:"model,omitempty"`
	Tools         ToolPolicy `yaml:"tools,omitempty" json:"tools,omitempty"`
	SubagentTypes []string   `yaml:"subagent_types,omitempty" json:"subagent_types,omitempty"`
}

// ResolvedSpec stores one merged spec after inheritance resolution.
type ResolvedSpec struct {
	Name             string   `json:"name"`
	SystemPrompt     string   `json:"system_prompt,omitempty"`
	Model            string   `json:"model,omitempty"`
	AllowedTools     []string `json:"allowed_tools,omitempty"`
	ExcludedTools    []string `json:"excluded_tools,omitempty"`
	SubagentTypes    []string `json:"subagent_types,omitempty"`
	SourcePath       string   `json:"source_path,omitempty"`
	InheritanceChain []string `json:"inheritance_chain,omitempty"`
}

// LoadAgentSpec loads one YAML file into raw AgentSpec.
func LoadAgentSpec(path string) (*AgentSpec, error) {
	resolvedPath, err := normalizeSpecPath(path)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("agentspec: read %q: %w", resolvedPath, err)
	}

	var spec AgentSpec
	if err := yaml.Unmarshal(content, &spec); err != nil {
		return nil, fmt.Errorf("agentspec: parse %q: %w", resolvedPath, err)
	}

	normalizeAgentSpec(&spec)
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("agentspec: %q: name is required", resolvedPath)
	}
	return &spec, nil
}

// Load is a shorthand alias of LoadAgentSpec.
func Load(path string) (*AgentSpec, error) {
	return LoadAgentSpec(path)
}

// ResolveAgentSpec resolves one spec file and all parent inheritance.
func ResolveAgentSpec(path string) (*ResolvedSpec, error) {
	resolvedPath, err := normalizeSpecPath(path)
	if err != nil {
		return nil, err
	}

	stack := map[string]struct{}{}
	cache := map[string]*AgentSpec{}
	chain := make([]string, 0, 4)
	rootDir := filepath.Dir(resolvedPath)
	specs, err := resolveSpecChain(resolvedPath, rootDir, stack, cache, &chain)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("agentspec: %q resolved empty spec chain", resolvedPath)
	}

	resolved := &ResolvedSpec{
		SourcePath:       resolvedPath,
		InheritanceChain: append([]string(nil), chain...),
	}
	for i := range specs {
		applySpecLayer(resolved, specs[i])
	}
	if strings.TrimSpace(resolved.Name) == "" {
		return nil, fmt.Errorf("agentspec: %q resolved empty name", resolvedPath)
	}
	if err := validateResolvedSpec(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// Resolve is a shorthand alias of ResolveAgentSpec.
func Resolve(path string) (*ResolvedSpec, error) {
	return ResolveAgentSpec(path)
}

// LoadResolvedSpec resolves inheritance and returns one merged spec.
func LoadResolvedSpec(path string) (*ResolvedSpec, error) {
	return ResolveAgentSpec(path)
}

func resolveSpecChain(
	path string,
	rootDir string,
	stack map[string]struct{},
	cache map[string]*AgentSpec,
	chain *[]string,
) ([]*AgentSpec, error) {
	if _, ok := stack[path]; ok {
		cycle := append(append([]string(nil), (*chain)...), path)
		return nil, fmt.Errorf("agentspec: inheritance cycle detected: %s", strings.Join(cycle, " -> "))
	}

	spec, ok := cache[path]
	if !ok {
		loaded, err := LoadAgentSpec(path)
		if err != nil {
			return nil, err
		}
		spec = loaded
		cache[path] = spec
	}

	stack[path] = struct{}{}
	*chain = append(*chain, path)
	defer func() {
		delete(stack, path)
	}()

	parentPath := strings.TrimSpace(spec.Extends)
	if parentPath == "" {
		return []*AgentSpec{spec}, nil
	}
	if filepath.IsAbs(parentPath) {
		return nil, fmt.Errorf("agentspec: extends path %q must be relative", parentPath)
	}

	resolvedParentPath := resolveExtendsPath(path, parentPath)
	if !pathWithinDir(resolvedParentPath, rootDir) {
		return nil, fmt.Errorf(
			"agentspec: extends path %q escapes root spec directory %q",
			parentPath,
			rootDir,
		)
	}
	parentChain, err := resolveSpecChain(resolvedParentPath, rootDir, stack, cache, chain)
	if err != nil {
		return nil, err
	}
	return append(parentChain, spec), nil
}

func applySpecLayer(dst *ResolvedSpec, layer *AgentSpec) {
	if dst == nil || layer == nil {
		return
	}
	if name := strings.TrimSpace(layer.Name); name != "" {
		dst.Name = name
	}
	if prompt := strings.TrimSpace(layer.SystemPrompt); prompt != "" {
		dst.SystemPrompt = prompt
	}
	if model := strings.TrimSpace(layer.Model); model != "" {
		dst.Model = model
	}

	toolPolicy := normalizeToolPolicy(layer.Tools)
	dst.AllowedTools = mergeStringLists(dst.AllowedTools, toolPolicy.AllowedTools)
	dst.ExcludedTools = mergeStringLists(dst.ExcludedTools, toolPolicy.ExcludedTools)
	dst.SubagentTypes = mergeStringLists(dst.SubagentTypes, layer.SubagentTypes)
}

func validateResolvedSpec(spec *ResolvedSpec) error {
	if spec == nil {
		return errors.New("agentspec: nil resolved spec")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("agentspec: resolved name is required")
	}

	if len(spec.AllowedTools) == 0 || len(spec.ExcludedTools) == 0 {
		return nil
	}

	excluded := make(map[string]struct{}, len(spec.ExcludedTools))
	for i := range spec.ExcludedTools {
		excluded[spec.ExcludedTools[i]] = struct{}{}
	}
	conflicts := make([]string, 0)
	for i := range spec.AllowedTools {
		if _, ok := excluded[spec.AllowedTools[i]]; ok {
			conflicts = append(conflicts, spec.AllowedTools[i])
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("agentspec: tools present in both allow/exclude: %s", strings.Join(conflicts, ", "))
}

func normalizeAgentSpec(spec *AgentSpec) {
	if spec == nil {
		return
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Extends = strings.TrimSpace(spec.Extends)
	spec.SystemPrompt = strings.TrimSpace(spec.SystemPrompt)
	spec.Model = strings.TrimSpace(spec.Model)
	spec.Tools = normalizeToolPolicy(spec.Tools)
	spec.SubagentTypes = normalizeStringList(spec.SubagentTypes)
}

func normalizeToolPolicy(policy ToolPolicy) ToolPolicy {
	allowed := make([]string, 0, len(policy.AllowedTools)+len(policy.Allow))
	allowed = append(allowed, policy.AllowedTools...)
	allowed = append(allowed, policy.Allow...)

	excluded := make([]string, 0, len(policy.ExcludedTools)+len(policy.Exclude))
	excluded = append(excluded, policy.ExcludedTools...)
	excluded = append(excluded, policy.Exclude...)

	return ToolPolicy{
		AllowedTools:  normalizeStringList(allowed),
		ExcludedTools: normalizeStringList(excluded),
	}
}

func mergeStringLists(base, overlays []string) []string {
	if len(base) == 0 && len(overlays) == 0 {
		return nil
	}
	merged := make([]string, 0, len(base)+len(overlays))
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
		merged = append(merged, name)
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
		merged = append(merged, name)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func normalizeStringList(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for i := range raw {
		item := strings.TrimSpace(raw[i])
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveExtendsPath(childPath, extends string) string {
	extends = strings.TrimSpace(extends)
	if extends == "" {
		return ""
	}
	if filepath.IsAbs(extends) {
		return filepath.Clean(extends)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(childPath), extends))
}

func normalizeSpecPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("agentspec: path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("agentspec: resolve path %q: %w", path, err)
	}
	return filepath.Clean(absPath), nil
}

func pathWithinDir(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(os.PathSeparator))
}
