package session

import (
	"bufio"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	kimiDirName             = ".kimi"
	sessionsDirName         = "sessions"
	lastSessionIDFileName   = "last_session_id"
	contextFileName         = "context.jsonl"
	wireFileName            = "wire.jsonl"
	subagentsDirName        = "subagents"
	tasksDirName            = "tasks"
	jsonlScannerMaxCapacity = 16 * 1024 * 1024
)

// Session stores one persisted conversation workspace.
type Session struct {
	ID          string
	WorkDir     string
	Dir         string
	ContextFile string
	WireFile    string
	State       *SessionState
	Title       string
	UpdatedAt   time.Time
}

// Create allocates and initializes one new session under {workDir}/.kimi/sessions/{id}.
func Create(workDir string) (*Session, error) {
	absWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return nil, err
	}

	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(sessionsRoot(absWorkDir), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session create: mkdir %q: %w", dir, err)
	}

	if err := os.MkdirAll(filepath.Join(dir, subagentsDirName), 0o755); err != nil {
		return nil, fmt.Errorf("session create: mkdir subagents dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, tasksDirName), 0o755); err != nil {
		return nil, fmt.Errorf("session create: mkdir tasks dir: %w", err)
	}

	contextPath := filepath.Join(dir, contextFileName)
	if err := ensureFile(contextPath); err != nil {
		return nil, fmt.Errorf("session create: init %q: %w", contextPath, err)
	}
	wirePath := filepath.Join(dir, wireFileName)
	if err := ensureFile(wirePath); err != nil {
		return nil, fmt.Errorf("session create: init %q: %w", wirePath, err)
	}

	session := &Session{
		ID:          sessionID,
		WorkDir:     absWorkDir,
		Dir:         dir,
		ContextFile: contextPath,
		WireFile:    wirePath,
		State:       NewSessionState(),
		Title:       sessionID,
	}
	if err := session.SaveState(); err != nil {
		return nil, fmt.Errorf("session create: save state: %w", err)
	}
	if err := writeLastSessionID(absWorkDir, sessionID); err != nil {
		return nil, fmt.Errorf("session create: save last session id: %w", err)
	}

	updatedAt, err := sessionUpdatedAt(dir)
	if err != nil {
		return nil, err
	}
	session.UpdatedAt = updatedAt
	return session, nil
}

// Find restores one existing session by ID.
func Find(workDir, sessionID string) (*Session, error) {
	absWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return nil, err
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session find: empty session id")
	}

	dir := filepath.Join(sessionsRoot(absWorkDir), sessionID)
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("session find: %q not found", sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("session find: stat %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("session find: %q is not a directory", dir)
	}

	state, err := LoadSessionState(dir)
	if err != nil {
		return nil, fmt.Errorf("session find: load state: %w", err)
	}

	updatedAt, err := sessionUpdatedAt(dir)
	if err != nil {
		return nil, err
	}

	return &Session{
		ID:          sessionID,
		WorkDir:     absWorkDir,
		Dir:         dir,
		ContextFile: filepath.Join(dir, contextFileName),
		WireFile:    filepath.Join(dir, wireFileName),
		State:       state,
		Title:       sessionID,
		UpdatedAt:   updatedAt,
	}, nil
}

// Continue restores the latest session pointed to by last_session_id.
func Continue(workDir string) (*Session, error) {
	absWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return nil, err
	}

	sessionID, err := readLastSessionID(absWorkDir)
	if err != nil {
		return nil, fmt.Errorf("session continue: %w", err)
	}
	return Find(absWorkDir, sessionID)
}

// List returns all sessions sorted by UpdatedAt in descending order.
func List(workDir string) ([]*Session, error) {
	absWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(sessionsRoot(absWorkDir))
	if errors.Is(err, os.ErrNotExist) {
		return []*Session{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session list: read session root: %w", err)
	}

	sessions := make([]*Session, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}

		session, err := Find(absWorkDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("session list: load %q: %w", entry.Name(), err)
		}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// SubagentsDir returns the subagents working directory for this session.
func (s *Session) SubagentsDir() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.Dir, subagentsDirName)
}

// TasksDir returns the background tasks directory for this session.
func (s *Session) TasksDir() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.Dir, tasksDirName)
}

// IsEmpty reports whether both context and wire logs are empty.
func (s *Session) IsEmpty() bool {
	if s == nil {
		return true
	}
	return isJSONLEmpty(s.ContextFile) && isJSONLEmpty(s.WireFile)
}

// Delete removes session directory, and clears last_session_id if it points to this session.
func (s *Session) Delete() error {
	if s == nil {
		return errors.New("session delete: nil session")
	}
	dir := strings.TrimSpace(s.Dir)
	if dir == "" {
		return errors.New("session delete: empty session dir")
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("session delete: remove %q: %w", dir, err)
	}

	workDir := strings.TrimSpace(s.WorkDir)
	if workDir == "" {
		return nil
	}
	lastSessionID, err := readLastSessionID(workDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("session delete: read last session id: %w", err)
	}
	if lastSessionID != s.ID {
		return nil
	}
	if err := os.Remove(lastSessionIDPath(workDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("session delete: remove last session id: %w", err)
	}
	return nil
}

// SaveState persists session state into the session directory.
func (s *Session) SaveState() error {
	if s == nil {
		return errors.New("session save state: nil session")
	}
	if strings.TrimSpace(s.Dir) == "" {
		return errors.New("session save state: empty session dir")
	}
	if s.State == nil {
		s.State = NewSessionState()
	}
	if err := s.State.Save(s.Dir); err != nil {
		return fmt.Errorf("session save state: %w", err)
	}

	updatedAt, err := sessionUpdatedAt(s.Dir)
	if err != nil {
		return err
	}
	s.UpdatedAt = updatedAt
	return nil
}

func resolveWorkDir(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("session: resolve working directory: %w", err)
		}
		workDir = cwd
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("session: resolve work dir %q: %w", workDir, err)
	}
	return filepath.Clean(absWorkDir), nil
}

func sessionsRoot(workDir string) string {
	return filepath.Join(workDir, kimiDirName, sessionsDirName)
}

func lastSessionIDPath(workDir string) string {
	return filepath.Join(sessionsRoot(workDir), lastSessionIDFileName)
}

func writeLastSessionID(workDir, sessionID string) error {
	if err := os.MkdirAll(sessionsRoot(workDir), 0o755); err != nil {
		return fmt.Errorf("session: mkdir session root: %w", err)
	}
	if err := os.WriteFile(lastSessionIDPath(workDir), []byte(sessionID+"\n"), 0o644); err != nil {
		return fmt.Errorf("session: write last session id: %w", err)
	}
	return nil
}

func readLastSessionID(workDir string) (string, error) {
	data, err := os.ReadFile(lastSessionIDPath(workDir))
	if err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(string(data))
	if sessionID == "" {
		return "", errors.New("last session id is empty")
	}
	return sessionID, nil
}

func ensureFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

func isJSONLEmpty(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), jsonlScannerMaxCapacity)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			return false
		}
	}
	return scanner.Err() == nil
}

func sessionUpdatedAt(dir string) (time.Time, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return time.Time{}, errors.New("session: empty session dir")
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, fmt.Errorf("session: stat %q: %w", dir, err)
	}
	updatedAt := dirInfo.ModTime()

	for _, name := range []string{contextFileName, wireFileName, stateFileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil {
			if info.ModTime().After(updatedAt) {
				updatedAt = info.ModTime()
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return time.Time{}, fmt.Errorf("session: stat %q: %w", filepath.Join(dir, name), err)
		}
	}
	return updatedAt, nil
}

func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("session: generate id: %w", err)
	}

	// RFC4122 variant + version 4.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
