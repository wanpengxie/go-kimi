// Package sandbox provides the manager-agent style "execute(name, input) → string"
// abstraction for go-kimi's standard tool set (shell / read_file / write_file /
// str_replace / grep / glob / read_media_file).
//
// Background — the manager-agent pattern
//
//	"each hand is a tool, execute(name, input) → string. That interface
//	supports any custom tool, any MCP server, and our own tools."
//	— https://www.anthropic.com/engineering/managed-agents
//
// The agent (LLM + harness) only sees a stable tool catalog (Name +
// Description + ParameterSchema). It is unaware of where execution actually
// happens — in this process, in a docker container, on a phone, or somewhere
// else over the wire. SandboxBackend is the single seam that lets users plug
// in any execution layer behind go-kimi's standard tool set:
//
//   - LocalBackend      → in-process execution via existing tools/shell + tools/file
//   - cloudagent's RemoteBackend → forwards every Execute over a brain↔hand wire
//   - other adopters    → docker / E2B / Modal / SSH / your own
//
// brain (the agent) treats them identically.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"

	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

// SandboxBackend is the single seam used by go-kimi's standard tool set
// (shell / read_file / write_file / str_replace / grep / glob /
// read_media_file). Any implementation can plug in to provide execution.
//
// Contract:
//
//   - name is one of the canonical standard tool names exported from this
//     package as Spec.Name (ShellSpec.Name, ReadFileSpec.Name, …).
//     Implementations may return ErrUnknownTool for names they do not handle.
//   - input is the raw JSON parameter payload as the LLM produced it.
//   - output is the LLM-facing string (typically the tool's stdout / file
//     content / search hits — whatever the user wants the model to see).
//   - isError signals a tool-level error (the LLM gets it via tool_result
//     is_error=true and is expected to react). Set isError=true and put the
//     error message in output for tool-level failures.
//   - err signals a transport-level / fatal failure (no message arrives).
//     Returning a non-nil err interrupts the agent loop; the caller decides
//     whether to fail-fast.
type SandboxBackend interface {
	Execute(ctx context.Context, name string, input json.RawMessage) (output string, isError bool, err error)
}

// Spec is the public metadata used to construct the tools.Tool wrappers from a
// backend (Name / Description / JSONSchema). The standard tool specs in this
// package are populated at init time from the live tool implementations in
// tools/shell + tools/file so they stay in lock-step with LocalBackend.
type Spec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ErrUnknownTool is returned by SandboxBackend.Execute when name is not a
// standard tool name the backend knows how to handle.
var ErrUnknownTool = fmt.Errorf("sandbox: unknown standard tool")

// ErrBackendDisconnected is re-exported from the kimi errors package so
// callers can keep referencing it from the package that conceptually owns
// the SandboxBackend interface (this one). The sentinel itself lives in
// pkg/kimi/errors purely to avoid an import cycle with internal/soul, which
// must short-circuit on this error without importing the sandbox package.
//
// Wrap it from a SandboxBackend implementation when the execution substrate
// is permanently lost for this run (the remote sandbox WebSocket dropped,
// the docker container died and we cannot reprovision, the SSH session
// closed). Soul.Run sees the chain via errors.Is and exits the agent loop
// without feeding the error back to the LLM — the model has no recovery
// action available, so retrying just wastes tokens.
//
// Wrap discipline: keep this sentinel narrow. Recoverable conditions
// (timeout, transient network blip, schema mismatch the model can fix on
// the next try) should flow through (output, isError=true, nil) so the LLM
// can adapt. Only wrap conditions where no tool call can possibly help.
var ErrBackendDisconnected = kimierrors.ErrBackendDisconnected

// toolReturnValueToString converts a types.ToolReturnValue into the LLM-facing
// string. Most go-kimi tools place a plain string in Value already; for any
// other shape we fall back to JSON-encoding so nothing silently disappears.
func toolReturnValueToString(v types.ToolReturnValue) string {
	if v.Value == nil {
		return ""
	}
	if s, ok := v.Value.(string); ok {
		return s
	}
	encoded, err := json.Marshal(v.Value)
	if err != nil {
		return fmt.Sprintf("%v", v.Value)
	}
	return string(encoded)
}
