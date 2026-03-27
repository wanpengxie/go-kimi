package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestCreateInitializesSessionLayout(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	session, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !sessionIDPattern.MatchString(session.ID) {
		t.Fatalf("Create().ID = %q, want UUID v4 format", session.ID)
	}
	if session.WorkDir != workDir {
		t.Fatalf("Create().WorkDir = %q, want %q", session.WorkDir, workDir)
	}
	if !session.IsEmpty() {
		t.Fatal("new session should be empty")
	}

	assertPathIsFile(t, session.ContextFile)
	assertPathIsFile(t, session.WireFile)
	assertPathIsDir(t, session.SubagentsDir())
	assertPathIsDir(t, session.TasksDir())

	state, err := LoadSessionState(session.Dir)
	if err != nil {
		t.Fatalf("LoadSessionState() error = %v", err)
	}
	if !reflect.DeepEqual(state, NewSessionState()) {
		t.Fatalf("state after Create() = %#v, want %#v", state, NewSessionState())
	}

	lastID, err := os.ReadFile(filepath.Join(workDir, ".kimi", "sessions", lastSessionIDFileName))
	if err != nil {
		t.Fatalf("os.ReadFile(last_session_id) error = %v", err)
	}
	if got := strings.TrimSpace(string(lastID)); got != session.ID {
		t.Fatalf("last_session_id = %q, want %q", got, session.ID)
	}
}

func TestFindRestoresSavedState(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	created, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	created.State.Yolo = false
	created.State.AutoApproveActions["shell.exec"] = true
	created.State.AdditionalDirs = []string{"/tmp/extra"}
	if err := created.SaveState(); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	found, err := Find(workDir, created.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if found.ID != created.ID {
		t.Fatalf("Find().ID = %q, want %q", found.ID, created.ID)
	}
	if !reflect.DeepEqual(found.State, created.State) {
		t.Fatalf("Find().State = %#v, want %#v", found.State, created.State)
	}
}

func TestContinueLoadsLastSession(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	first, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	got, err := Continue(workDir)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("Continue().ID = %q, want %q", got.ID, second.ID)
	}
	if got.ID == first.ID {
		t.Fatalf("Continue() restored first session %q, want latest %q", first.ID, second.ID)
	}
}

func TestContinueWithoutLastSessionIDFails(t *testing.T) {
	t.Parallel()

	_, err := Continue(t.TempDir())
	if err == nil {
		t.Fatal("Continue() without last_session_id should fail")
	}
}

func TestFindRejectsPathTraversalSessionID(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	testCases := []string{
		"../..",
		"../../etc/passwd",
		".",
	}
	for _, sessionID := range testCases {
		sessionID := sessionID
		t.Run(sessionID, func(t *testing.T) {
			t.Parallel()

			_, err := Find(workDir, sessionID)
			if err == nil {
				t.Fatalf("Find(%q) error = nil, want error", sessionID)
			}
			if !strings.Contains(err.Error(), "invalid session id") {
				t.Fatalf("Find(%q) error = %v, want invalid session id", sessionID, err)
			}
		})
	}
}

func TestContinueRejectsPoisonedLastSessionID(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if _, err := Create(workDir); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lastSessionIDPath := filepath.Join(workDir, ".kimi", "sessions", lastSessionIDFileName)
	if err := os.WriteFile(lastSessionIDPath, []byte("../../etc/passwd\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(poisoned last_session_id) error = %v", err)
	}

	_, err := Continue(workDir)
	if err == nil {
		t.Fatal("Continue(poisoned last_session_id) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("Continue(poisoned last_session_id) error = %v, want invalid session id", err)
	}
}

func TestListReturnsSessionsByUpdatedAtDesc(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	first, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	newer := time.Now().Add(2 * time.Hour)
	older := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filepath.Join(first.Dir, stateFileName), newer, newer); err != nil {
		t.Fatalf("os.Chtimes(first state) error = %v", err)
	}
	if err := os.Chtimes(filepath.Join(second.Dir, stateFileName), older, older); err != nil {
		t.Fatalf("os.Chtimes(second state) error = %v", err)
	}

	got, err := List(workDir)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("List() order = [%q, %q], want [%q, %q]", got[0].ID, got[1].ID, first.ID, second.ID)
	}
}

func TestListMissingSessionRootReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(got))
	}
}

func TestIsEmptyIgnoresBlankLines(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	session, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := os.WriteFile(session.ContextFile, []byte("\n  \n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(context blank) error = %v", err)
	}
	if err := os.WriteFile(session.WireFile, []byte("\n\t\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(wire blank) error = %v", err)
	}
	if !session.IsEmpty() {
		t.Fatal("IsEmpty() should ignore blank lines")
	}

	if err := os.WriteFile(session.ContextFile, []byte("{\"role\":\"user\"}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(context payload) error = %v", err)
	}
	if session.IsEmpty() {
		t.Fatal("IsEmpty() should be false once context has content")
	}
}

func TestDeleteRemovesSessionAndPointer(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	first, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := Create(workDir)
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	if err := first.Delete(); err != nil {
		t.Fatalf("Delete(first) error = %v", err)
	}
	lastID, err := os.ReadFile(filepath.Join(workDir, ".kimi", "sessions", lastSessionIDFileName))
	if err != nil {
		t.Fatalf("os.ReadFile(last_session_id) error = %v", err)
	}
	if got := strings.TrimSpace(string(lastID)); got != second.ID {
		t.Fatalf("last_session_id after deleting non-last = %q, want %q", got, second.ID)
	}

	if err := second.Delete(); err != nil {
		t.Fatalf("Delete(second) error = %v", err)
	}
	if _, err := os.Stat(second.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second session dir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".kimi", "sessions", lastSessionIDFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("last_session_id should be removed after deleting current session, stat err = %v", err)
	}
}

func TestDeleteRejectsTraversalSessionID(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	guardPath := filepath.Join(workDir, "guard-dir")
	if err := os.MkdirAll(guardPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", guardPath, err)
	}

	s := &Session{
		ID:      "../guard-dir",
		WorkDir: workDir,
		Dir:     guardPath,
	}
	err := s.Delete()
	if err == nil {
		t.Fatal("Delete(traversal session id) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("Delete(traversal session id) error = %v, want invalid session id", err)
	}
	if _, err := os.Stat(guardPath); err != nil {
		t.Fatalf("guard path should remain after rejected delete, stat err = %v", err)
	}
}

func assertPathIsFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("path %q is directory, want file", path)
	}
}

func assertPathIsDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("path %q is file, want directory", path)
	}
}
