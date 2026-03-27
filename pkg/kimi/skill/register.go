package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const skillToolPrefix = "skill:"

var skillParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "args": {
      "type": "string",
      "description": "Optional extra arguments passed to the skill prompt"
    }
  },
  "additionalProperties": false
}`)

// RegisterSkills registers every discovered skill as a model-callable tool:
// skill:<name>.
func RegisterSkills(s *soul.Soul, skills map[string]*Skill) {
	if s == nil || len(skills) == 0 {
		return
	}

	registry := ensureSkillRegistry(s)
	if registry == nil {
		return
	}

	normalized := make(map[string]*Skill, len(skills))
	for key, candidate := range skills {
		if candidate == nil {
			continue
		}

		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			name = strings.TrimSpace(key)
		}
		if name == "" {
			continue
		}

		normalized[name] = candidate
	}

	names := make([]string, 0, len(normalized))
	for name := range normalized {
		names = append(names, name)
	}
	sort.Strings(names)

	for i := range names {
		name := names[i]
		sk := normalized[name]
		toolName := skillToolName(name)

		registry.register(
			toolName,
			llm.ToolDefinition{
				Name:        toolName,
				Description: strings.TrimSpace(sk.Description),
				Parameters:  decodeSchema(skillParameterSchema),
			},
			&SkillRunner{
				soul:  s,
				skill: sk,
				runMu: &registry.runMu,
			},
		)
	}
}

// SkillRunner executes one skill tool invocation.
type SkillRunner struct {
	soul  *soul.Soul
	skill *Skill
	runMu *sync.Mutex
}

// Execute runs the skill prompt via soul.Run and returns the nested assistant output.
func (r *SkillRunner) Execute(ctx context.Context, call types.ToolCall) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if r == nil || r.soul == nil || r.skill == nil {
		return toolError(call, "skill runner is not initialized"), nil
	}

	args, err := decodeSkillArgs(call.Arguments)
	if err != nil {
		return toolError(call, fmt.Sprintf("decode args: %v", err)), nil
	}

	input := buildSkillInput(r.skill.Content, args)
	if strings.TrimSpace(input) == "" {
		return toolError(call, "skill content is empty"), nil
	}

	if r.runMu != nil {
		r.runMu.Lock()
		defer r.runMu.Unlock()
	}

	runResult, runErr := r.soul.Run(ctx, types.ContentParts{
		types.TextPart{Text: input},
	})
	if runErr != nil {
		return toolError(call, fmt.Sprintf("run skill %q: %v", r.skill.Name, runErr)), nil
	}

	return types.ToolResult{
		ToolCallID: strings.TrimSpace(call.ID),
		Name:       strings.TrimSpace(call.Name),
		Value: types.ToolReturnValue{
			Value: contentPartsText(runResult.Content),
		},
	}, nil
}

type skillRegistry struct {
	base       soul.ToolRegistry
	defs       map[string]llm.ToolDefinition
	executors  map[string]soul.ToolExecutor
	runMu      sync.Mutex
	registerMu sync.RWMutex
}

func ensureSkillRegistry(s *soul.Soul) *skillRegistry {
	if s == nil {
		return nil
	}

	current := s.ToolRegistry()
	if existing, ok := current.(*skillRegistry); ok {
		return existing
	}

	wrapped := &skillRegistry{
		base:      current,
		defs:      make(map[string]llm.ToolDefinition),
		executors: make(map[string]soul.ToolExecutor),
	}
	s.SetToolRegistry(wrapped)
	return wrapped
}

func (r *skillRegistry) register(name string, definition llm.ToolDefinition, executor soul.ToolExecutor) {
	name = strings.TrimSpace(name)
	if r == nil || name == "" || executor == nil {
		return
	}

	r.registerMu.Lock()
	defer r.registerMu.Unlock()

	if r.defs == nil {
		r.defs = make(map[string]llm.ToolDefinition)
	}
	if r.executors == nil {
		r.executors = make(map[string]soul.ToolExecutor)
	}

	definition.Name = name
	r.defs[name] = definition
	r.executors[name] = executor
}

func (r *skillRegistry) Definitions() []llm.ToolDefinition {
	if r == nil {
		return nil
	}

	merged := make(map[string]llm.ToolDefinition)
	if r.base != nil {
		baseDefs := r.base.Definitions()
		for i := range baseDefs {
			name := strings.TrimSpace(baseDefs[i].Name)
			if name == "" {
				continue
			}
			baseDefs[i].Name = name
			merged[name] = baseDefs[i]
		}
	}

	r.registerMu.RLock()
	for name, definition := range r.defs {
		merged[name] = definition
	}
	r.registerMu.RUnlock()

	if len(merged) == 0 {
		return nil
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]llm.ToolDefinition, 0, len(names))
	for i := range names {
		defs = append(defs, merged[names[i]])
	}
	return defs
}

func (r *skillRegistry) Executor(name string) (soul.ToolExecutor, bool) {
	name = strings.TrimSpace(name)
	if r == nil || name == "" {
		return nil, false
	}

	r.registerMu.RLock()
	executor, ok := r.executors[name]
	r.registerMu.RUnlock()
	if ok {
		return executor, true
	}

	if r.base == nil {
		return nil, false
	}
	return r.base.Executor(name)
}

func skillToolName(name string) string {
	return skillToolPrefix + strings.TrimSpace(name)
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

func decodeSkillArgs(arguments types.JsonType) (string, error) {
	raw, err := marshalArguments(arguments)
	if err != nil {
		return "", err
	}

	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" || text == "{}" {
		return "", nil
	}

	var payload struct {
		Args string `json:"args"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload.Args, nil
	}

	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain, nil
	}

	return "", fmt.Errorf("invalid args payload %q", text)
}

func marshalArguments(arguments types.JsonType) (json.RawMessage, error) {
	switch typed := arguments.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		return normalizeRawJSON(typed)
	case []byte:
		return normalizeRawJSON(json.RawMessage(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return json.RawMessage(`{}`), nil
		}
		if json.Valid([]byte(trimmed)) {
			return normalizeRawJSON(json.RawMessage(trimmed))
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return normalizeRawJSON(encoded)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return normalizeRawJSON(encoded)
	}
}

func normalizeRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("invalid json %q", trimmed)
	}
	out := make(json.RawMessage, len(trimmed))
	copy(out, trimmed)
	return out, nil
}

func buildSkillInput(content, args string) string {
	content = strings.TrimSpace(content)
	args = strings.TrimSpace(args)

	switch {
	case content == "" && args == "":
		return ""
	case content == "":
		return args
	case args == "":
		return content
	default:
		return content + "\n\nArgs:\n" + args
	}
}

func contentPartsText(parts types.ContentParts) string {
	var builder strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			builder.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				builder.WriteString(typed.Text)
			}
		case types.ThinkPart:
			builder.WriteString(typed.Think)
		case *types.ThinkPart:
			if typed != nil {
				builder.WriteString(typed.Think)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func toolError(call types.ToolCall, message string) types.ToolResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "skill"
	}
	return types.ToolResult{
		ToolCallID: strings.TrimSpace(call.ID),
		Name:       name,
		Value: types.ToolReturnValue{
			Value: message,
		},
		IsError: true,
	}
}
