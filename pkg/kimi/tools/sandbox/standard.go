package sandbox

import (
	"context"
	"encoding/json"

	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

// StandardToolsFromBackend wraps the supplied SandboxBackend into a slice of
// tools.Tool ready to register on a kimi.Agent. One tools.Tool is produced for
// every standard spec in AllSpecs() — the agent sees the same tool catalog
// regardless of whether the backend executes locally, over a wire, or on a
// Pokémon emulator.
func StandardToolsFromBackend(backend SandboxBackend) []tools.Tool {
	specs := AllSpecs()
	out := make([]tools.Tool, 0, len(specs))
	for _, spec := range specs {
		out = append(out, &standardTool{backend: backend, spec: spec})
	}
	return out
}

// standardTool is the private adapter that satisfies tools.Tool by delegating
// every Execute to the backing SandboxBackend. The Name / Description /
// ParameterSchema reported to the LLM come from a fixed Spec, decoupling the
// agent-visible interface from the underlying execution.
type standardTool struct {
	backend SandboxBackend
	spec    Spec
}

func (t *standardTool) Name() string { return t.spec.Name }

func (t *standardTool) Description() string { return t.spec.Description }

func (t *standardTool) ParameterSchema() json.RawMessage { return t.spec.InputSchema }

func (t *standardTool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	output, isErr, err := t.backend.Execute(ctx, t.spec.Name, params)
	if err != nil {
		return types.ToolResult{}, err
	}
	return types.ToolResult{
		Name:    t.spec.Name,
		Value:   types.ToolReturnValue{Value: output},
		IsError: isErr,
	}, nil
}
