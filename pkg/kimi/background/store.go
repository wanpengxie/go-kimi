package background

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	taskSpecFileName    = "spec.json"
	taskRuntimeFileName = "runtime.json"
	taskControlFileName = "control.json"
	taskOutputFileName  = "output.log"
	taskConsumersDir    = "consumers"
	MaxTaskOutputBytes  = 1 * 1024 * 1024
)

// BackgroundTaskStore persists background tasks under one session tasks directory.
type BackgroundTaskStore struct {
	dir string
}

// NewBackgroundTaskStore creates one store rooted at session/tasks/.
func NewBackgroundTaskStore(dir string) *BackgroundTaskStore {
	return &BackgroundTaskStore{dir: dir}
}

// Create allocates one task directory and writes initial spec/runtime/control files.
func (s *BackgroundTaskStore) Create(spec TaskSpec) error {
	baseDir, err := s.storeDir()
	if err != nil {
		return err
	}

	normalizedSpec, err := normalizeTaskSpec(spec)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("background store: mkdir %q: %w", baseDir, err)
	}

	taskDir := filepath.Join(baseDir, normalizedSpec.ID)
	if err := os.Mkdir(taskDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("background store: task %q already exists: %w", normalizedSpec.ID, os.ErrExist)
		}
		return fmt.Errorf("background store: mkdir %q: %w", taskDir, err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(taskDir)
		}
	}()

	if err := writeJSONAtomic(filepath.Join(taskDir, taskSpecFileName), &normalizedSpec); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(taskDir, taskRuntimeFileName), &TaskRuntime{Status: TaskCreated}); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(taskDir, taskControlFileName), &TaskControl{}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(taskDir, taskOutputFileName), nil, 0o644); err != nil {
		return fmt.Errorf("background store: write %q: %w", filepath.Join(taskDir, taskOutputFileName), err)
	}

	cleanup = false
	return nil
}

// ReadSpec loads one task spec.
func (s *BackgroundTaskStore) ReadSpec(taskID string) (*TaskSpec, error) {
	path, err := s.taskFilePath(taskID, taskSpecFileName)
	if err != nil {
		return nil, err
	}

	var spec TaskSpec
	if err := readJSON(path, &spec); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return nil, err
	}

	normalized, err := normalizeTaskSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("background store: invalid spec %q: %w", path, err)
	}
	return &normalized, nil
}

// ReadRuntime loads one task runtime snapshot.
func (s *BackgroundTaskStore) ReadRuntime(taskID string) (*TaskRuntime, error) {
	path, err := s.taskFilePath(taskID, taskRuntimeFileName)
	if err != nil {
		return nil, err
	}

	var runtime TaskRuntime
	if err := readJSON(path, &runtime); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return nil, err
	}

	normalized := normalizeRuntime(runtime)
	return &normalized, nil
}

// WriteRuntime atomically rewrites one task runtime snapshot.
func (s *BackgroundTaskStore) WriteRuntime(taskID string, rt *TaskRuntime) error {
	if rt == nil {
		return errors.New("background store: runtime is nil")
	}
	normalizedTaskID, err := s.ensureTaskExists(taskID)
	if err != nil {
		return err
	}

	path, err := s.taskFilePath(normalizedTaskID, taskRuntimeFileName)
	if err != nil {
		return err
	}

	normalized := normalizeRuntime(*rt)
	if err := writeJSONAtomic(path, &normalized); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return err
	}
	return nil
}

// ReadControl loads one task control snapshot.
func (s *BackgroundTaskStore) ReadControl(taskID string) (*TaskControl, error) {
	path, err := s.taskFilePath(taskID, taskControlFileName)
	if err != nil {
		return nil, err
	}

	var control TaskControl
	if err := readJSON(path, &control); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return nil, err
	}

	normalized := normalizeControl(control)
	return &normalized, nil
}

// WriteControl atomically rewrites one task control snapshot.
func (s *BackgroundTaskStore) WriteControl(taskID string, ctrl *TaskControl) error {
	if ctrl == nil {
		return errors.New("background store: control is nil")
	}
	normalizedTaskID, err := s.ensureTaskExists(taskID)
	if err != nil {
		return err
	}

	path, err := s.taskFilePath(normalizedTaskID, taskControlFileName)
	if err != nil {
		return err
	}

	normalized := normalizeControl(*ctrl)
	if err := writeJSONAtomic(path, &normalized); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return err
	}
	return nil
}

// View aggregates one task spec/runtime/control snapshot.
func (s *BackgroundTaskStore) View(taskID string) (*TaskView, error) {
	spec, err := s.ReadSpec(taskID)
	if err != nil {
		return nil, err
	}
	runtime, err := s.ReadRuntime(taskID)
	if err != nil {
		return nil, err
	}
	control, err := s.ReadControl(taskID)
	if err != nil {
		return nil, err
	}
	return &TaskView{
		Spec:    *spec,
		Runtime: *runtime,
		Control: *control,
	}, nil
}

// ListViews lists all task views under the store directory.
func (s *BackgroundTaskStore) ListViews() ([]*TaskView, error) {
	baseDir, err := s.storeDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return []*TaskView{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("background store: read dir %q: %w", baseDir, err)
	}

	views := make([]*TaskView, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}

		view, err := s.View(entry.Name())
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].Spec.ID < views[j].Spec.ID
	})
	return views, nil
}

// ReadOutput reads one task output slice starting from offset.
func (s *BackgroundTaskStore) ReadOutput(taskID string, offset int64, maxBytes int) ([]byte, error) {
	if offset < 0 {
		return nil, errors.New("background store: offset must be >= 0")
	}
	if maxBytes < 0 {
		return nil, errors.New("background store: maxBytes must be >= 0")
	}

	path, err := s.taskFilePath(taskID, taskOutputFileName)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return nil, fmt.Errorf("background store: open %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("background store: seek %q to %d: %w", path, offset, err)
	}

	effectiveMaxBytes := maxBytes
	switch {
	case effectiveMaxBytes <= 0:
		effectiveMaxBytes = MaxTaskOutputBytes
	case effectiveMaxBytes > MaxTaskOutputBytes:
		effectiveMaxBytes = MaxTaskOutputBytes
	}
	reader := io.LimitReader(file, int64(effectiveMaxBytes))
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("background store: read %q: %w", path, err)
	}
	return data, nil
}

// AppendOutput appends bytes to one task output log.
func (s *BackgroundTaskStore) AppendOutput(taskID string, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	path, err := s.taskFilePath(taskID, taskOutputFileName)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return fmt.Errorf("background store: open %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("background store: append %q: %w", path, err)
	}
	return nil
}

// OutputSize returns one task output log size in bytes.
func (s *BackgroundTaskStore) OutputSize(taskID string) (int64, error) {
	path, err := s.taskFilePath(taskID, taskOutputFileName)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("background store: task %q not found: %w", strings.TrimSpace(taskID), os.ErrNotExist)
		}
		return 0, fmt.Errorf("background store: stat %q: %w", path, err)
	}
	return info.Size(), nil
}

// ReadConsumerState loads one consumer cursor; missing state returns offset=0.
func (s *BackgroundTaskStore) ReadConsumerState(taskID string, consumerID string) (*TaskConsumerState, error) {
	normalizedTaskID, err := s.ensureTaskExists(taskID)
	if err != nil {
		return nil, err
	}
	normalizedConsumerID, err := normalizeConsumerID(consumerID)
	if err != nil {
		return nil, err
	}
	path, err := s.consumerStatePath(normalizedTaskID, normalizedConsumerID)
	if err != nil {
		return nil, err
	}

	var state TaskConsumerState
	if err := readJSON(path, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &TaskConsumerState{
				TaskID:     normalizedTaskID,
				ConsumerID: normalizedConsumerID,
				Offset:     0,
			}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(state.TaskID) == "" {
		state.TaskID = normalizedTaskID
	}
	if strings.TrimSpace(state.TaskID) != normalizedTaskID {
		return nil, fmt.Errorf("background store: consumer state task_id mismatch %q != %q", strings.TrimSpace(state.TaskID), normalizedTaskID)
	}
	if strings.TrimSpace(state.ConsumerID) == "" {
		state.ConsumerID = normalizedConsumerID
	}
	if strings.TrimSpace(state.ConsumerID) != normalizedConsumerID {
		return nil, fmt.Errorf("background store: consumer state consumer_id mismatch %q != %q", strings.TrimSpace(state.ConsumerID), normalizedConsumerID)
	}
	if state.Offset < 0 {
		return nil, errors.New("background store: consumer state offset must be >= 0")
	}
	return &TaskConsumerState{
		TaskID:     normalizedTaskID,
		ConsumerID: normalizedConsumerID,
		Offset:     state.Offset,
	}, nil
}

// WriteConsumerState persists one consumer cursor snapshot.
func (s *BackgroundTaskStore) WriteConsumerState(taskID string, state *TaskConsumerState) error {
	if state == nil {
		return errors.New("background store: consumer state is nil")
	}
	normalizedTaskID, err := s.ensureTaskExists(taskID)
	if err != nil {
		return err
	}
	normalizedConsumerID, err := normalizeConsumerID(state.ConsumerID)
	if err != nil {
		return err
	}
	if state.Offset < 0 {
		return errors.New("background store: consumer state offset must be >= 0")
	}

	path, err := s.consumerStatePath(normalizedTaskID, normalizedConsumerID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("background store: mkdir %q: %w", filepath.Dir(path), err)
	}
	normalized := TaskConsumerState{
		TaskID:     normalizedTaskID,
		ConsumerID: normalizedConsumerID,
		Offset:     state.Offset,
	}
	return writeJSONAtomic(path, &normalized)
}

func (s *BackgroundTaskStore) storeDir() (string, error) {
	if s == nil {
		return "", errors.New("background store: nil store")
	}

	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return "", errors.New("background store: store dir is empty")
	}
	return filepath.Clean(dir), nil
}

func (s *BackgroundTaskStore) taskFilePath(taskID, fileName string) (string, error) {
	baseDir, err := s.storeDir()
	if err != nil {
		return "", err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, normalizedTaskID, fileName), nil
}

func (s *BackgroundTaskStore) ensureTaskExists(taskID string) (string, error) {
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	specPath, err := s.taskFilePath(normalizedTaskID, taskSpecFileName)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(specPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("background store: task %q not found: %w", normalizedTaskID, os.ErrNotExist)
		}
		return "", fmt.Errorf("background store: stat %q: %w", specPath, err)
	}
	return normalizedTaskID, nil
}

func normalizeTaskSpec(spec TaskSpec) (TaskSpec, error) {
	normalized := TaskSpec{
		ID:            strings.TrimSpace(spec.ID),
		Kind:          TaskKind(strings.TrimSpace(string(spec.Kind))),
		SessionID:     strings.TrimSpace(spec.SessionID),
		Description:   strings.TrimSpace(spec.Description),
		ToolCallID:    strings.TrimSpace(spec.ToolCallID),
		Command:       strings.TrimSpace(spec.Command),
		WorkDir:       strings.TrimSpace(spec.WorkDir),
		TimeoutSec:    spec.TimeoutSec,
		AgentID:       strings.TrimSpace(spec.AgentID),
		SubagentType:  strings.TrimSpace(spec.SubagentType),
		Prompt:        strings.TrimSpace(spec.Prompt),
		ModelOverride: strings.TrimSpace(spec.ModelOverride),
	}

	taskID, err := normalizeTaskID(normalized.ID)
	if err != nil {
		return TaskSpec{}, err
	}
	normalized.ID = taskID

	if normalized.TimeoutSec < 0 {
		return TaskSpec{}, errors.New("background store: timeout_s must be >= 0")
	}
	switch normalized.Kind {
	case TaskKindBash:
		if normalized.Command == "" {
			return TaskSpec{}, errors.New("background store: command is required for bash task")
		}
	case TaskKindAgent:
		if normalized.Prompt == "" {
			return TaskSpec{}, errors.New("background store: prompt is required for agent task")
		}
	default:
		return TaskSpec{}, fmt.Errorf("background store: unsupported task kind %q", normalized.Kind)
	}

	return normalized, nil
}

func normalizeTaskID(taskID string) (string, error) {
	normalized := strings.TrimSpace(taskID)
	if normalized == "" {
		return "", errors.New("background store: task id is required")
	}
	if normalized == "." || normalized == ".." {
		return "", fmt.Errorf("background store: invalid task id %q", normalized)
	}
	if strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") {
		return "", fmt.Errorf("background store: invalid task id %q", normalized)
	}
	return normalized, nil
}

func normalizeConsumerID(consumerID string) (string, error) {
	normalized := strings.TrimSpace(consumerID)
	if normalized == "" {
		return "", errors.New("background store: consumer id is required")
	}
	if normalized == "." || normalized == ".." {
		return "", fmt.Errorf("background store: invalid consumer id %q", normalized)
	}
	if strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") {
		return "", fmt.Errorf("background store: invalid consumer id %q", normalized)
	}
	return normalized, nil
}

func (s *BackgroundTaskStore) consumerStatePath(taskID string, consumerID string) (string, error) {
	baseDir, err := s.storeDir()
	if err != nil {
		return "", err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	normalizedConsumerID, err := normalizeConsumerID(consumerID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, normalizedTaskID, taskConsumersDir, normalizedConsumerID+".json"), nil
}

func normalizeRuntime(rt TaskRuntime) TaskRuntime {
	rt.Status = TaskStatus(strings.TrimSpace(string(rt.Status)))
	if rt.Status == "" {
		rt.Status = TaskCreated
	}
	rt.FailureReason = strings.TrimSpace(rt.FailureReason)
	return rt
}

func normalizeControl(ctrl TaskControl) TaskControl {
	ctrl.KillReason = strings.TrimSpace(ctrl.KillReason)
	return ctrl
}

func readJSON(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("background store: read %q: %w", path, err)
	}

	if strings.TrimSpace(string(payload)) == "" {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("background store: decode %q: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("background store: marshal %q: %w", path, err)
	}
	payload = append(payload, '\n')

	tmpFile, err := os.CreateTemp(dir, "task-*.tmp")
	if err != nil {
		return fmt.Errorf("background store: create temp for %q: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("background store: write temp %q: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("background store: close temp %q: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("background store: chmod temp %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("background store: rename %q -> %q: %w", tmpPath, path, err)
	}
	return nil
}
