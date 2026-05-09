package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

func TestMapToolRegistryDefinitionsSortedAndDecoded(t *testing.T) {
	t.Parallel()

	registry := NewMapToolRegistry(
		&stubTool{
			name:        "zeta",
			description: "z tool",
			schema: json.RawMessage(`{
				"type": "object",
				"properties": {"query": {"type": "string"}}
			}`),
		},
		&stubTool{
			name:        "alpha",
			description: "a tool",
			schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
	)

	definitions := registry.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("len(Definitions) = %d, want 2", len(definitions))
	}
	if definitions[0].Name != "alpha" || definitions[1].Name != "zeta" {
		t.Fatalf("Definitions names = [%q %q], want [alpha zeta]", definitions[0].Name, definitions[1].Name)
	}

	params, ok := definitions[0].Parameters.(map[string]any)
	if !ok {
		t.Fatalf("definitions[0].Parameters type = %T, want map[string]any", definitions[0].Parameters)
	}
	if got, _ := params["type"].(string); got != "object" {
		t.Fatalf("definitions[0].Parameters.type = %q, want object", got)
	}
}

func TestMapToolRegistryExecutorUsesToolAdapter(t *testing.T) {
	t.Parallel()

	tool := &stubTool{
		name: "echo",
		result: types.ToolResult{
			Name: "echo",
			Value: types.ToolReturnValue{
				Value: "ok",
			},
		},
	}
	registry := NewMapToolRegistry(tool)

	executor, ok := registry.Executor(" echo ")
	if !ok {
		t.Fatal("Executor( echo ) ok = false, want true")
	}

	_, err := executor.Execute(context.Background(), types.ToolCall{
		Name: "echo",
		Arguments: map[string]any{
			"message": "hello",
			"count":   1,
		},
	})
	if err != nil {
		t.Fatalf("executor.Execute() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(tool.lastParams, &got); err != nil {
		t.Fatalf("json.Unmarshal(lastParams) error = %v", err)
	}
	if got["message"] != "hello" {
		t.Fatalf("lastParams.message = %#v, want hello", got["message"])
	}
}

func TestMapToolRegistryExecutorMissingTool(t *testing.T) {
	t.Parallel()

	registry := NewMapToolRegistry()
	executor, ok := registry.Executor("missing")
	if ok {
		t.Fatalf("Executor(missing) ok = true, want false (executor=%#v)", executor)
	}
}

func TestToolAdapterRejectsInvalidJSONStringArguments(t *testing.T) {
	t.Parallel()

	tool := &stubTool{name: "demo"}
	executor := NewToolAdapter(tool)
	_, err := executor.Execute(context.Background(), types.ToolCall{
		Name:      "demo",
		Arguments: `{"broken_json": `,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid json") {
		t.Fatalf("Execute() error = %v, want invalid json error", err)
	}
}

func TestToolAdapterNilArgumentsBecomesEmptyObject(t *testing.T) {
	t.Parallel()

	tool := &stubTool{
		name: "demo",
		result: types.ToolResult{
			Name: "demo",
			Value: types.ToolReturnValue{
				Value: "ok",
			},
		},
	}
	executor := NewToolAdapter(tool)

	if _, err := executor.Execute(context.Background(), types.ToolCall{Name: "demo"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(tool.lastParams) != "{}" {
		t.Fatalf("lastParams = %q, want {}", string(tool.lastParams))
	}
}

type stubTool struct {
	name        string
	description string
	schema      json.RawMessage
	lastParams  json.RawMessage
	result      types.ToolResult
	err         error
}

func (t *stubTool) Name() string {
	return t.name
}

func (t *stubTool) Description() string {
	return t.description
}

func (t *stubTool) ParameterSchema() json.RawMessage {
	out := make(json.RawMessage, len(t.schema))
	copy(out, t.schema)
	return out
}

func (t *stubTool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	t.lastParams = append(t.lastParams[:0], params...)
	if t.err != nil {
		return types.ToolResult{}, t.err
	}
	return t.result, nil
}
