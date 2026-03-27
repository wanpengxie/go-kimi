package background

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackgroundTaskStoreCreateReadWriteListAndOutput(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "tasks")
	store := NewBackgroundTaskStore(root)

	spec1 := TaskSpec{
		ID:          "task-1",
		Kind:        TaskKindBash,
		SessionID:   "session-1",
		Description: "run one command",
		ToolCallID:  "call-1",
		Command:     "echo hello",
		WorkDir:     "/tmp",
		TimeoutSec:  9,
	}
	if err := store.Create(spec1); err != nil {
		t.Fatalf("Create(task-1) error = %v", err)
	}

	for _, file := range []string{taskSpecFileName, taskRuntimeFileName, taskControlFileName, taskOutputFileName} {
		path := filepath.Join(root, "task-1", file)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("os.Stat(%q) error = %v", path, err)
		}
	}

	readSpec, err := store.ReadSpec("task-1")
	if err != nil {
		t.Fatalf("ReadSpec(task-1) error = %v", err)
	}
	if readSpec.ID != "task-1" {
		t.Fatalf("ReadSpec(task-1).ID = %q, want task-1", readSpec.ID)
	}
	if readSpec.Kind != TaskKindBash {
		t.Fatalf("ReadSpec(task-1).Kind = %q, want %q", readSpec.Kind, TaskKindBash)
	}
	if readSpec.Command != "echo hello" {
		t.Fatalf("ReadSpec(task-1).Command = %q, want %q", readSpec.Command, "echo hello")
	}

	readRuntime, err := store.ReadRuntime("task-1")
	if err != nil {
		t.Fatalf("ReadRuntime(task-1) error = %v", err)
	}
	if readRuntime.Status != TaskCreated {
		t.Fatalf("ReadRuntime(task-1).Status = %q, want %q", readRuntime.Status, TaskCreated)
	}

	startedAt := 12.34
	exitCode := 0
	readRuntime.Status = TaskCompleted
	readRuntime.StartedAt = &startedAt
	readRuntime.HeartbeatAt = &startedAt
	readRuntime.FinishedAt = &startedAt
	readRuntime.ExitCode = &exitCode
	if err := store.WriteRuntime("task-1", readRuntime); err != nil {
		t.Fatalf("WriteRuntime(task-1) error = %v", err)
	}

	control := &TaskControl{
		KillRequestedAt: &startedAt,
		KillReason:      "manual stop",
	}
	if err := store.WriteControl("task-1", control); err != nil {
		t.Fatalf("WriteControl(task-1) error = %v", err)
	}

	if err := store.AppendOutput("task-1", []byte("hello\n")); err != nil {
		t.Fatalf("AppendOutput(task-1, hello) error = %v", err)
	}
	if err := store.AppendOutput("task-1", []byte("world")); err != nil {
		t.Fatalf("AppendOutput(task-1, world) error = %v", err)
	}

	output, err := store.ReadOutput("task-1", 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput(task-1, 0, 0) error = %v", err)
	}
	if string(output) != "hello\nworld" {
		t.Fatalf("ReadOutput(task-1, 0, 0) = %q, want %q", string(output), "hello\nworld")
	}

	window, err := store.ReadOutput("task-1", 1, 4)
	if err != nil {
		t.Fatalf("ReadOutput(task-1, 1, 4) error = %v", err)
	}
	if string(window) != "ello" {
		t.Fatalf("ReadOutput(task-1, 1, 4) = %q, want %q", string(window), "ello")
	}

	view, err := store.View("task-1")
	if err != nil {
		t.Fatalf("View(task-1) error = %v", err)
	}
	if view.Spec.ID != "task-1" {
		t.Fatalf("View(task-1).Spec.ID = %q, want task-1", view.Spec.ID)
	}
	if view.Runtime.Status != TaskCompleted {
		t.Fatalf("View(task-1).Runtime.Status = %q, want %q", view.Runtime.Status, TaskCompleted)
	}
	if view.Control.KillReason != "manual stop" {
		t.Fatalf("View(task-1).Control.KillReason = %q, want %q", view.Control.KillReason, "manual stop")
	}

	spec2 := TaskSpec{
		ID:         "task-2",
		Kind:       TaskKindAgent,
		Prompt:     "help me",
		TimeoutSec: 3,
	}
	if err := store.Create(spec2); err != nil {
		t.Fatalf("Create(task-2) error = %v", err)
	}

	list, err := store.ListViews()
	if err != nil {
		t.Fatalf("ListViews() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(ListViews()) = %d, want 2", len(list))
	}
	if list[0].Spec.ID != "task-1" || list[1].Spec.ID != "task-2" {
		t.Fatalf("ListViews() ids = [%q %q], want [task-1 task-2]", list[0].Spec.ID, list[1].Spec.ID)
	}
}

func TestBackgroundTaskStoreListOnMissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := NewBackgroundTaskStore(filepath.Join(t.TempDir(), "missing", "tasks"))
	list, err := store.ListViews()
	if err != nil {
		t.Fatalf("ListViews() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("len(ListViews()) = %d, want 0", len(list))
	}
}

func TestBackgroundTaskStoreValidationAndMissingErrors(t *testing.T) {
	t.Parallel()

	store := NewBackgroundTaskStore(filepath.Join(t.TempDir(), "tasks"))

	if err := store.Create(TaskSpec{ID: "../bad", Kind: TaskKindBash, Command: "echo hi"}); err == nil {
		t.Fatal("Create(path traversal) error = nil, want error")
	}
	if err := store.Create(TaskSpec{ID: "task-bad-kind", Kind: TaskKind("x"), Command: "echo hi"}); err == nil {
		t.Fatal("Create(bad kind) error = nil, want error")
	}
	if err := store.Create(TaskSpec{ID: "task-no-command", Kind: TaskKindBash}); err == nil {
		t.Fatal("Create(missing command) error = nil, want error")
	}
	if err := store.Create(TaskSpec{ID: "task-no-prompt", Kind: TaskKindAgent}); err == nil {
		t.Fatal("Create(missing prompt) error = nil, want error")
	}

	if _, err := store.ReadSpec("missing"); err == nil {
		t.Fatal("ReadSpec(missing) error = nil, want error")
	}
	if _, err := store.ReadRuntime("missing"); err == nil {
		t.Fatal("ReadRuntime(missing) error = nil, want error")
	}
	if _, err := store.ReadControl("missing"); err == nil {
		t.Fatal("ReadControl(missing) error = nil, want error")
	}
	if _, err := store.View("missing"); err == nil {
		t.Fatal("View(missing) error = nil, want error")
	}
	if _, err := store.ReadOutput("missing", -1, 0); err == nil {
		t.Fatal("ReadOutput(negative offset) error = nil, want error")
	}
	if _, err := store.ReadOutput("missing", 0, -1); err == nil {
		t.Fatal("ReadOutput(negative maxBytes) error = nil, want error")
	}
	if err := store.WriteRuntime("missing", nil); err == nil {
		t.Fatal("WriteRuntime(nil) error = nil, want error")
	}
	if err := store.WriteControl("missing", nil); err == nil {
		t.Fatal("WriteControl(nil) error = nil, want error")
	}
	if err := store.AppendOutput("missing", []byte("x")); err == nil {
		t.Fatal("AppendOutput(missing) error = nil, want error")
	}
}

func TestBackgroundTaskStoreWriteRequiresExistingTask(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "tasks")
	store := NewBackgroundTaskStore(root)

	if err := store.WriteRuntime("missing-task", &TaskRuntime{Status: TaskRunning}); err == nil {
		t.Fatal("WriteRuntime(missing-task, non-nil) error = nil, want error")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WriteRuntime(missing-task, non-nil) error = %v, want os.ErrNotExist", err)
	}

	if err := store.WriteControl("missing-task", &TaskControl{KillReason: "manual"}); err == nil {
		t.Fatal("WriteControl(missing-task, non-nil) error = nil, want error")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WriteControl(missing-task, non-nil) error = %v, want os.ErrNotExist", err)
	}

	ghostDir := filepath.Join(root, "missing-task")
	if _, err := os.Stat(ghostDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ghost dir %q exists unexpectedly (stat err=%v)", ghostDir, err)
	}
}

func TestBackgroundTaskStoreReadOutputHardCap(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "tasks")
	store := NewBackgroundTaskStore(root)
	if err := store.Create(TaskSpec{
		ID:      "task-1",
		Kind:    TaskKindBash,
		Command: "echo hi",
	}); err != nil {
		t.Fatalf("Create(task-1) error = %v", err)
	}

	payload := strings.Repeat("a", MaxTaskOutputBytes+64)
	if err := store.AppendOutput("task-1", []byte(payload)); err != nil {
		t.Fatalf("AppendOutput(task-1) error = %v", err)
	}

	defaultRead, err := store.ReadOutput("task-1", 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput(task-1, 0, 0) error = %v", err)
	}
	if len(defaultRead) != MaxTaskOutputBytes {
		t.Fatalf("len(ReadOutput default) = %d, want %d", len(defaultRead), MaxTaskOutputBytes)
	}

	oversizedRead, err := store.ReadOutput("task-1", 0, MaxTaskOutputBytes*10)
	if err != nil {
		t.Fatalf("ReadOutput(task-1, 0, oversized) error = %v", err)
	}
	if len(oversizedRead) != MaxTaskOutputBytes {
		t.Fatalf("len(ReadOutput oversized) = %d, want %d", len(oversizedRead), MaxTaskOutputBytes)
	}

	windowRead, err := store.ReadOutput("task-1", 0, 128)
	if err != nil {
		t.Fatalf("ReadOutput(task-1, 0, 128) error = %v", err)
	}
	if len(windowRead) != 128 {
		t.Fatalf("len(ReadOutput window) = %d, want 128", len(windowRead))
	}
}

func TestBackgroundTaskStoreCorruptedRecordAndNilStore(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "tasks")
	store := NewBackgroundTaskStore(root)
	if err := os.MkdirAll(filepath.Join(root, "task-corrupt"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(task-corrupt) error = %v", err)
	}
	specPath := filepath.Join(root, "task-corrupt", taskSpecFileName)
	if err := os.WriteFile(specPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(spec.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "task-corrupt", taskRuntimeFileName), []byte("{}"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(runtime.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "task-corrupt", taskControlFileName), []byte("{}"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(control.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "task-corrupt", taskOutputFileName), nil, 0o644); err != nil {
		t.Fatalf("os.WriteFile(output.log) error = %v", err)
	}

	if _, err := store.ListViews(); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("ListViews() error = %v, want decode context", err)
	}

	var nilStore *BackgroundTaskStore
	if _, err := nilStore.ListViews(); err == nil {
		t.Fatal("nil store ListViews() error = nil, want error")
	}

	emptyStore := NewBackgroundTaskStore("   ")
	if _, err := emptyStore.ListViews(); err == nil {
		t.Fatal("empty dir ListViews() error = nil, want error")
	}
}
