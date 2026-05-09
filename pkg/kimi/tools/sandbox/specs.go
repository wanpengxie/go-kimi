package sandbox

import (
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/file"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/shell"
)

// Standard tool specs. Populated at init time from live tool instances so they
// always match the canonical Name / Description / ParameterSchema exposed by
// tools/shell + tools/file. Keeping the source of truth in the tool packages
// means any update there automatically propagates here — no duplication, no
// drift.
//
// Names are stable contract; LocalBackend + RemoteBackend (cloudagent) +
// any other implementation must dispatch on these exact names.
var (
	ShellSpec         Spec
	ReadFileSpec      Spec
	WriteFileSpec     Spec
	StrReplaceSpec    Spec
	GrepSpec          Spec
	GlobSpec          Spec
	ReadMediaFileSpec Spec
)

// AllSpecs returns the canonical ordering used everywhere in this package
// (factories, default registrations). The order is stable so consumers can
// rely on a predictable iteration order when they enumerate the standard set.
func AllSpecs() []Spec {
	return []Spec{
		ShellSpec,
		ReadFileSpec,
		WriteFileSpec,
		StrReplaceSpec,
		GrepSpec,
		GlobSpec,
		ReadMediaFileSpec,
	}
}

// AllSpecNames returns the standard tool names in canonical order.
func AllSpecNames() []string {
	specs := AllSpecs()
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}

func init() {
	// Pull live spec out of each tool implementation. Constructor args here
	// (workDir / approver / vision flags) are throwaway — we only call the
	// metadata methods, never Execute.
	ShellSpec = specOf(shell.New("", nil))
	ReadFileSpec = specOf(file.NewReadFile(""))
	WriteFileSpec = specOf(file.NewWriteFile("", nil))
	StrReplaceSpec = specOf(file.NewStrReplace("", nil))
	GrepSpec = specOf(file.NewGrep(""))
	GlobSpec = specOf(file.NewGlob(""))
	ReadMediaFileSpec = specOf(file.NewReadMediaFile("", true, true))
}

func specOf(t tools.Tool) Spec {
	return Spec{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: t.ParameterSchema(),
	}
}
