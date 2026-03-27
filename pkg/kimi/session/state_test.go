package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadSessionStateMissingReturnsDefault(t *testing.T) {
	t.Parallel()

	got, err := LoadSessionState(t.TempDir())
	if err != nil {
		t.Fatalf("LoadSessionState() error = %v", err)
	}

	want := NewSessionState()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadSessionState() = %#v, want %#v", got, want)
	}
}

func TestSessionStateSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := &SessionState{
		Version: 2,
		Yolo:    false,
		AutoApproveActions: map[string]bool{
			"shell.exec": true,
			"file.write": false,
		},
		AdditionalDirs: []string{
			"/tmp/data",
			"/tmp/out",
		},
	}

	if err := state.Save(dir); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := LoadSessionState(dir)
	if err != nil {
		t.Fatalf("LoadSessionState() error = %v", err)
	}

	want := normalizeSessionState(state)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("load after save = %#v, want %#v", got, want)
	}
}

func TestSessionStateSaveRejectsEmptyDir(t *testing.T) {
	t.Parallel()

	err := NewSessionState().Save("  ")
	if err == nil {
		t.Fatal("Save() with empty dir should fail")
	}
	if !strings.Contains(err.Error(), "dir is empty") {
		t.Fatalf("Save() error = %v, want dir is empty", err)
	}
}

func TestLoadSessionStateInvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{bad"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := LoadSessionState(dir)
	if err == nil {
		t.Fatal("LoadSessionState() should fail on invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("LoadSessionState() error = %v, want parse context", err)
	}
}
