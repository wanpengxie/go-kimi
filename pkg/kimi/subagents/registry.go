package subagents

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// LaborMarket stores known subagent type definitions.
type LaborMarket struct {
	mu    sync.RWMutex
	types map[string]*AgentTypeDefinition
}

// NewLaborMarket creates one empty subagent type registry.
func NewLaborMarket() *LaborMarket {
	return &LaborMarket{
		types: make(map[string]*AgentTypeDefinition),
	}
}

// Register adds or replaces one subagent type definition.
func (m *LaborMarket) Register(def *AgentTypeDefinition) {
	if m == nil || def == nil {
		return
	}

	cloned, ok := cloneTypeDefinition(def)
	if !ok {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.types == nil {
		m.types = make(map[string]*AgentTypeDefinition)
	}
	m.types[cloned.Name] = cloned
}

// Get returns one subagent type definition by name.
func (m *LaborMarket) Get(name string) (*AgentTypeDefinition, bool) {
	name = strings.TrimSpace(name)
	if m == nil || name == "" {
		return nil, false
	}

	m.mu.RLock()
	definition, ok := m.types[name]
	m.mu.RUnlock()
	if !ok || definition == nil {
		return nil, false
	}

	cloned, _ := cloneTypeDefinition(definition)
	return cloned, true
}

// Require returns one subagent type definition or a descriptive error.
func (m *LaborMarket) Require(name string) (*AgentTypeDefinition, error) {
	normalized := strings.TrimSpace(name)
	definition, ok := m.Get(normalized)
	if !ok {
		return nil, fmt.Errorf("subagents: type %q not found", normalized)
	}
	return definition, nil
}

// List returns all registered subagent type definitions sorted by name.
func (m *LaborMarket) List() []*AgentTypeDefinition {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	names := make([]string, 0, len(m.types))
	for name := range m.types {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]*AgentTypeDefinition, 0, len(names))
	for i := range names {
		definition, _ := cloneTypeDefinition(m.types[names[i]])
		if definition != nil {
			definitions = append(definitions, definition)
		}
	}
	m.mu.RUnlock()

	return definitions
}

func cloneTypeDefinition(def *AgentTypeDefinition) (*AgentTypeDefinition, bool) {
	if def == nil {
		return nil, false
	}

	name := strings.TrimSpace(def.Name)
	if name == "" {
		return nil, false
	}

	cloned := *def
	cloned.Name = name
	cloned.Description = strings.TrimSpace(cloned.Description)
	cloned.WhenToUse = strings.TrimSpace(cloned.WhenToUse)
	cloned.DefaultModel = strings.TrimSpace(cloned.DefaultModel)
	if len(cloned.ToolPolicy.Allowlist) > 0 {
		cloned.ToolPolicy.Allowlist = append([]string(nil), cloned.ToolPolicy.Allowlist...)
	}
	return &cloned, true
}
