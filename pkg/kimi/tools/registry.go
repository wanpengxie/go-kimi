package tools

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/wanpengxie/go-kimi/internal/soul"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

// MapToolRegistry stores tools in a name-keyed map.
type MapToolRegistry struct {
	tools map[string]Tool
}

// NewMapToolRegistry creates a map-backed tool registry.
func NewMapToolRegistry(tools ...Tool) *MapToolRegistry {
	registry := &MapToolRegistry{
		tools: make(map[string]Tool, len(tools)),
	}
	for i := range tools {
		registry.Register(tools[i])
	}
	return registry
}

// Register adds or replaces one tool by name.
func (r *MapToolRegistry) Register(t Tool) {
	if r == nil || t == nil {
		return
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return
	}
	if r.tools == nil {
		r.tools = map[string]Tool{}
	}
	r.tools[name] = t
}

// Definitions returns model-facing tool definitions.
func (r *MapToolRegistry) Definitions() []llm.ToolDefinition {
	if r == nil || len(r.tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]llm.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		definitions = append(definitions, llm.ToolDefinition{
			Name:        name,
			Description: strings.TrimSpace(tool.Description()),
			Parameters:  decodeSchema(tool.ParameterSchema()),
		})
	}
	return definitions
}

// Executor returns one tool executor by name.
func (r *MapToolRegistry) Executor(name string) (soul.ToolExecutor, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[strings.TrimSpace(name)]
	if !ok {
		return nil, false
	}
	return NewToolAdapter(tool), true
}

func decodeSchema(raw json.RawMessage) types.JsonType {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil
	}

	var schema types.JsonType
	if err := json.Unmarshal([]byte(text), &schema); err != nil {
		return nil
	}
	return schema
}
