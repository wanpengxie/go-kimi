package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	stateFileName       = "state.json"
	sessionStateVersion = 1
)

// SessionState stores persisted session-level runtime settings.
type SessionState struct {
	Version            int             `json:"version"`
	Yolo               bool            `json:"yolo"`
	AutoApproveActions map[string]bool `json:"auto_approve_actions,omitempty"`
	AdditionalDirs     []string        `json:"additional_dirs,omitempty"`
	PlanMode           bool            `json:"plan_mode,omitempty"`
	PlanSessionID      string          `json:"plan_session_id,omitempty"`
	PlanSlug           string          `json:"plan_slug,omitempty"`
}

// NewSessionState returns default session state.
func NewSessionState() *SessionState {
	return &SessionState{
		Version:            sessionStateVersion,
		Yolo:               true,
		AutoApproveActions: map[string]bool{},
	}
}

// LoadSessionState loads one session state from {dir}/state.json.
// Missing file is treated as default state.
func LoadSessionState(dir string) (*SessionState, error) {
	path, err := sessionStatePath(dir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewSessionState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("session state: read %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return NewSessionState(), nil
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("session state: parse %q: %w", path, err)
	}
	return normalizeSessionState(&state), nil
}

// Save persists session state into {dir}/state.json.
func (s *SessionState) Save(dir string) error {
	path, err := sessionStatePath(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("session state: mkdir %q: %w", filepath.Dir(path), err)
	}

	state := normalizeSessionState(s)
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("session state: marshal: %w", err)
	}
	payload = append(payload, '\n')

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "state-*.tmp")
	if err != nil {
		return fmt.Errorf("session state: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session state: write temp %q: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session state: close temp %q: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session state: chmod temp %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session state: rename %q -> %q: %w", tmpPath, path, err)
	}
	return nil
}

func normalizeSessionState(state *SessionState) *SessionState {
	if state == nil {
		return NewSessionState()
	}

	normalized := &SessionState{
		Version:            state.Version,
		Yolo:               state.Yolo,
		AutoApproveActions: make(map[string]bool, len(state.AutoApproveActions)),
		AdditionalDirs:     append([]string(nil), state.AdditionalDirs...),
		PlanMode:           state.PlanMode,
		PlanSessionID:      strings.TrimSpace(state.PlanSessionID),
		PlanSlug:           strings.TrimSpace(state.PlanSlug),
	}
	for action, approved := range state.AutoApproveActions {
		normalized.AutoApproveActions[action] = approved
	}

	if normalized.Version <= 0 {
		normalized.Version = sessionStateVersion
		// Pre-version states should preserve current non-blocking default.
		normalized.Yolo = true
	}
	return normalized
}

func sessionStatePath(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("session state: dir is empty")
	}
	return filepath.Join(dir, stateFileName), nil
}
