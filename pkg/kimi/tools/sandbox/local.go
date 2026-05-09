package sandbox

import (
	"context"
	"encoding/json"
	"fmt"

	corebg "github.com/wanpengxie/go-kimi/pkg/kimi/background"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/file"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/shell"
)

// LocalBackend implements SandboxBackend by routing each Execute call to the
// matching go-kimi standard tool implementation in tools/shell + tools/file.
// All execution happens in this process — this is the default backend used by
// kimi.NewAgent when no SandboxBackend is supplied.
//
// LocalBackend is just a router; it owns no logic of its own. Tool semantics,
// truncation, approval, background dispatch all live inside the underlying
// tool implementations.
type LocalBackend struct {
	registry map[string]tools.Tool
}

// LocalBackendOptions wires the host context that the standard tool
// implementations need (workspace dir, approver callback, background task
// manager, vision/video capability flags).
type LocalBackendOptions struct {
	WorkDir             string
	ShellApprover       shell.Approver
	FileApprover        file.Approver
	BackgroundManager   shell.BackgroundManager
	BackgroundSessionID string
	SupportsVision      bool
	SupportsVideo       bool
}

// NewLocalBackend constructs a LocalBackend with the standard tool set
// (shell + read_file + write_file + str_replace + grep + glob +
// read_media_file). Constructor flags map directly to the host context the
// underlying tools require; callers can leave any field zero and the
// corresponding tool falls back to its default behaviour (no approver, no
// background dispatch, vision/video disabled).
func NewLocalBackend(opts LocalBackendOptions) *LocalBackend {
	b := &LocalBackend{registry: map[string]tools.Tool{}}

	var sh tools.Tool
	if opts.BackgroundManager != nil {
		sh = shell.NewWithBackground(opts.WorkDir, opts.ShellApprover, opts.BackgroundManager, opts.BackgroundSessionID)
	} else {
		sh = shell.New(opts.WorkDir, opts.ShellApprover)
	}
	b.register(sh)
	b.register(file.NewReadFile(opts.WorkDir))
	b.register(file.NewWriteFile(opts.WorkDir, opts.FileApprover))
	b.register(file.NewStrReplace(opts.WorkDir, opts.FileApprover))
	b.register(file.NewGrep(opts.WorkDir))
	b.register(file.NewGlob(opts.WorkDir))
	if opts.SupportsVision || opts.SupportsVideo {
		b.register(file.NewReadMediaFile(opts.WorkDir, opts.SupportsVision, opts.SupportsVideo))
	}
	return b
}

// register installs a tool under its declared Name(). Last writer wins; if a
// caller explicitly overrides one of the standard tools after construction
// (via Replace) the new instance takes effect.
func (b *LocalBackend) register(t tools.Tool) {
	if t == nil {
		return
	}
	name := t.Name()
	if name == "" {
		return
	}
	b.registry[name] = t
}

// Replace swaps the implementation registered for one standard tool name. Use
// this when you need to inject a custom variant of one of the standard tools
// (e.g. a stricter shell wrapper) while keeping the rest of LocalBackend
// intact.
func (b *LocalBackend) Replace(t tools.Tool) {
	b.register(t)
}

// Execute routes by name to the underlying tool. Returns ErrUnknownTool when
// name is not in the registry; otherwise the tool's ToolResult is mapped to
// (output string, isError bool, err error).
func (b *LocalBackend) Execute(ctx context.Context, name string, input json.RawMessage) (string, bool, error) {
	t, ok := b.registry[name]
	if !ok {
		return "", false, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	res, err := t.Execute(ctx, input)
	if err != nil {
		return "", false, err
	}
	return toolReturnValueToString(res.Value), res.IsError, nil
}

// Names returns the names this LocalBackend currently dispatches. Useful when
// pairing LocalBackend with StandardToolsFromBackend to know which tools are
// actually wired up given the constructor flags (e.g. read_media_file is only
// present when SupportsVision || SupportsVideo).
func (b *LocalBackend) Names() []string {
	out := make([]string, 0, len(b.registry))
	for name := range b.registry {
		out = append(out, name)
	}
	return out
}

// Compile-time check.
var _ SandboxBackend = (*LocalBackend)(nil)

// reExportBackgroundManager is unused but keeps the corebg import alive when
// shell.BackgroundManager evolves — placeholder to surface compile-time
// breakage early instead of lint noise.
var _ = corebg.TaskSpec{}
