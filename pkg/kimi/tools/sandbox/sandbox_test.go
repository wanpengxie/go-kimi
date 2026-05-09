package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeBackend captures every Execute call so tests can assert that the
// standardTool wrapper delegates exactly once with the right name + payload.
type fakeBackend struct {
	calls []fakeCall
	out   string
	isErr bool
	err   error
}

type fakeCall struct {
	Name  string
	Input string
}

func (b *fakeBackend) Execute(_ context.Context, name string, input json.RawMessage) (string, bool, error) {
	b.calls = append(b.calls, fakeCall{Name: name, Input: string(input)})
	return b.out, b.isErr, b.err
}

// TestSpecsAreInitialised guards that init() actually populated each standard
// spec from the live tool implementations. If a downstream change in
// tools/shell or tools/file silently empties one of these specs the agent
// would publish a tool with no name/description/schema — catch it here.
func TestSpecsAreInitialised(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		spec  Spec
	}{
		{"shell", ShellSpec},
		{"read_file", ReadFileSpec},
		{"write_file", WriteFileSpec},
		{"str_replace", StrReplaceSpec},
		{"grep", GrepSpec},
		{"glob", GlobSpec},
		{"read_media_file", ReadMediaFileSpec},
	}
	for _, tc := range cases {
		if tc.spec.Name == "" {
			t.Errorf("%s: Name empty", tc.label)
		}
		if tc.spec.Description == "" {
			t.Errorf("%s: Description empty", tc.label)
		}
		if len(tc.spec.InputSchema) == 0 {
			t.Errorf("%s: InputSchema empty", tc.label)
		}
	}
}

func TestAllSpecsCanonicalOrder(t *testing.T) {
	t.Parallel()
	got := AllSpecNames()
	want := []string{"shell", "read_file", "write_file", "str_replace", "grep", "glob", "read_media_file"}
	if len(got) != len(want) {
		t.Fatalf("AllSpecNames len: got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("position %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestStandardToolsFromBackendDelegatesByName(t *testing.T) {
	t.Parallel()
	be := &fakeBackend{out: "ok"}
	all := StandardToolsFromBackend(be)
	if len(all) != len(AllSpecs()) {
		t.Fatalf("StandardToolsFromBackend len: got=%d want=%d", len(all), len(AllSpecs()))
	}

	// Pick the shell tool, run it, assert the backend got name="shell" + the
	// exact input payload.
	var shellTool int = -1
	for i, t0 := range all {
		if t0.Name() == ShellSpec.Name {
			shellTool = i
			break
		}
	}
	if shellTool < 0 {
		t.Fatalf("could not find shell tool by name")
	}

	input := json.RawMessage(`{"command":"ls"}`)
	res, err := all[shellTool].Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: unexpected err: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError=true, want false")
	}
	if got, ok := res.Value.Value.(string); !ok || got != "ok" {
		t.Errorf("Value.Value: got=%v want=ok", res.Value.Value)
	}
	if len(be.calls) != 1 {
		t.Fatalf("backend calls: got=%d want=1", len(be.calls))
	}
	if be.calls[0].Name != "shell" {
		t.Errorf("name: got=%q want=shell", be.calls[0].Name)
	}
	if be.calls[0].Input != `{"command":"ls"}` {
		t.Errorf("input passthrough mismatch: got=%q", be.calls[0].Input)
	}
}

func TestStandardToolErrorPropagation(t *testing.T) {
	t.Parallel()

	// tool-level error: isError=true gets surfaced on ToolResult, not as Go
	// error (so the LLM can react via tool_result is_error=true).
	tlErr := &fakeBackend{out: "boom", isErr: true}
	tools := StandardToolsFromBackend(tlErr)
	res, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool-level err should not surface as Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError: got=false want=true")
	}
	if got, _ := res.Value.Value.(string); got != "boom" {
		t.Errorf("Value: got=%v want=boom", res.Value.Value)
	}

	// transport-level error: Go error from backend becomes Go error from
	// tool.Execute (interrupts the agent loop).
	transportErr := &fakeBackend{err: errors.New("hand disconnected")}
	tools = StandardToolsFromBackend(transportErr)
	_, err = tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("transport err should surface as Go error")
	}
	if !strings.Contains(err.Error(), "hand disconnected") {
		t.Errorf("err message lost: got=%v", err)
	}
}

func TestLocalBackendUnknownTool(t *testing.T) {
	t.Parallel()
	be := NewLocalBackend(LocalBackendOptions{WorkDir: t.TempDir()})
	_, _, err := be.Execute(context.Background(), "no.such.tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected ErrUnknownTool")
	}
	if !errors.Is(err, ErrUnknownTool) {
		t.Errorf("wrong sentinel: got=%v want=%v", err, ErrUnknownTool)
	}
}

func TestLocalBackendNamesRespectsCapabilities(t *testing.T) {
	t.Parallel()

	// Without vision/video, read_media_file must not be registered.
	be := NewLocalBackend(LocalBackendOptions{WorkDir: t.TempDir()})
	for _, n := range be.Names() {
		if n == ReadMediaFileSpec.Name {
			t.Errorf("read_media_file leaked into LocalBackend without vision/video flags")
		}
	}

	// With at least one capability flag, it must appear.
	be = NewLocalBackend(LocalBackendOptions{WorkDir: t.TempDir(), SupportsVision: true})
	found := false
	for _, n := range be.Names() {
		if n == ReadMediaFileSpec.Name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("read_media_file missing when SupportsVision=true")
	}
}

func TestLocalBackendIsSandboxBackend(t *testing.T) {
	t.Parallel()
	var _ SandboxBackend = NewLocalBackend(LocalBackendOptions{})
}
