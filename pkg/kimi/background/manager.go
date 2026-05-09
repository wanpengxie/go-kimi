package background

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wanpengxie/go-kimi/pkg/kimi/subagents"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

var backgroundTaskSequence uint64

const waitPollInterval = 20 * time.Millisecond

// SubagentRunner executes one foreground subagent run.
type SubagentRunner interface {
	Run(ctx context.Context, req subagents.ForegroundRunRequest) (types.ToolReturnValue, error)
}

// ManagerDeps defines dependencies for BackgroundTaskManager.
type ManagerDeps struct {
	Store          *BackgroundTaskStore
	SubagentRunner SubagentRunner
}

// BackgroundTaskManager manages background task lifecycle.
type BackgroundTaskManager struct {
	store          *BackgroundTaskStore
	subagentRunner SubagentRunner

	mu           sync.Mutex
	closed       bool
	tasks        map[string]runningTask
	wg           sync.WaitGroup
	runtimeLocks sync.Map // map[taskID]*sync.Mutex, serializes runtime/control mutations per task.
}

type runningTask struct {
	cancel context.CancelFunc
}

// NewBackgroundTaskManager creates one manager with dependencies.
func NewBackgroundTaskManager(deps ManagerDeps) *BackgroundTaskManager {
	return &BackgroundTaskManager{
		store:          deps.Store,
		subagentRunner: deps.SubagentRunner,
		tasks:          make(map[string]runningTask),
	}
}

// CreateBashTask creates and starts one background bash task.
func (m *BackgroundTaskManager) CreateBashTask(ctx context.Context, spec TaskSpec) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.validateBaseDeps(); err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("background manager: create bash task: %w", ctx.Err())
	default:
	}

	normalizedSpec, err := normalizeBashCreateSpec(spec)
	if err != nil {
		return "", err
	}
	if err := m.store.Create(normalizedSpec); err != nil {
		return "", err
	}

	taskCtx, err := m.registerTask(normalizedSpec.ID)
	if err != nil {
		_ = m.finalizeRuntime(normalizedSpec.ID, runtimeFinalState{
			status:        TaskKilled,
			timedOut:      false,
			failureReason: "background manager is shut down",
		})
		return "", err
	}

	go func() {
		defer m.unregisterTask(normalizedSpec.ID)
		m.runBashTask(taskCtx, normalizedSpec)
	}()
	return normalizedSpec.ID, nil
}

// CreateAgentTask creates and starts one background agent task.
func (m *BackgroundTaskManager) CreateAgentTask(ctx context.Context, spec TaskSpec) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.validateBaseDeps(); err != nil {
		return "", err
	}
	if m.subagentRunner == nil {
		return "", errors.New("background manager: nil subagent runner")
	}

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("background manager: create agent task: %w", ctx.Err())
	default:
	}

	normalizedSpec, err := normalizeAgentCreateSpec(spec)
	if err != nil {
		return "", err
	}
	if err := m.store.Create(normalizedSpec); err != nil {
		return "", err
	}

	taskCtx, err := m.registerTask(normalizedSpec.ID)
	if err != nil {
		_ = m.finalizeRuntime(normalizedSpec.ID, runtimeFinalState{
			status:        TaskKilled,
			timedOut:      false,
			failureReason: "background manager is shut down",
		})
		return "", err
	}

	go func() {
		defer m.unregisterTask(normalizedSpec.ID)
		m.runAgentTask(taskCtx, normalizedSpec)
	}()
	return normalizedSpec.ID, nil
}

// GetTask returns one task view by id.
func (m *BackgroundTaskManager) GetTask(taskID string) (*TaskView, error) {
	if err := m.validateBaseDeps(); err != nil {
		return nil, err
	}
	return m.store.View(taskID)
}

// ListTasks lists persisted task views sorted by most recently updated.
func (m *BackgroundTaskManager) ListTasks(limit int) ([]*TaskView, error) {
	if err := m.validateBaseDeps(); err != nil {
		return nil, err
	}

	views, err := m.store.ListViews()
	if err != nil {
		return nil, err
	}
	sort.Slice(views, func(i, j int) bool {
		iKey := taskRecencyKey(views[i])
		jKey := taskRecencyKey(views[j])
		if iKey == jKey {
			return views[i].Spec.ID > views[j].Spec.ID
		}
		return iKey > jKey
	})

	if limit > 0 && len(views) > limit {
		return views[:limit], nil
	}
	return views, nil
}

// ReadOutput reads one output slice from the store.
func (m *BackgroundTaskManager) ReadOutput(taskID string, offset int64, maxBytes int) ([]byte, error) {
	if err := m.validateBaseDeps(); err != nil {
		return nil, err
	}
	return m.store.ReadOutput(taskID, offset, maxBytes)
}

// TailOutput reads one structured output chunk from offset with eof/status metadata.
func (m *BackgroundTaskManager) TailOutput(taskID string, offset int64, maxBytes int) (TaskOutputChunk, error) {
	if err := m.validateBaseDeps(); err != nil {
		return TaskOutputChunk{}, err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	if offset < 0 {
		return TaskOutputChunk{}, errors.New("background manager: offset must be >= 0")
	}

	view, err := m.store.View(normalizedTaskID)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	output, err := m.store.ReadOutput(normalizedTaskID, offset, maxBytes)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	totalSize, err := m.store.OutputSize(normalizedTaskID)
	if err != nil {
		return TaskOutputChunk{}, err
	}

	nextOffset := offset + int64(len(output))
	if nextOffset > totalSize {
		nextOffset = totalSize
	}
	eof := view.Runtime.Status.IsTerminal() && nextOffset >= totalSize

	return TaskOutputChunk{
		TaskID:     normalizedTaskID,
		Status:     view.Runtime.Status,
		Offset:     offset,
		NextOffset: nextOffset,
		Output:     string(output),
		EOF:        eof,
	}, nil
}

// ReadConsumerOutput tails output using one consumer-specific persisted cursor.
func (m *BackgroundTaskManager) ReadConsumerOutput(taskID string, consumerID string, maxBytes int) (TaskOutputChunk, error) {
	if err := m.validateBaseDeps(); err != nil {
		return TaskOutputChunk{}, err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	normalizedConsumerID, err := normalizeConsumerID(consumerID)
	if err != nil {
		return TaskOutputChunk{}, err
	}

	state, err := m.store.ReadConsumerState(normalizedTaskID, normalizedConsumerID)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	chunk, err := m.TailOutput(normalizedTaskID, state.Offset, maxBytes)
	if err != nil {
		return TaskOutputChunk{}, err
	}
	chunk.ConsumerID = normalizedConsumerID

	state.Offset = chunk.NextOffset
	if err := m.store.WriteConsumerState(normalizedTaskID, state); err != nil {
		return TaskOutputChunk{}, err
	}
	return chunk, nil
}

// Wait blocks until one task reaches terminal state or context is done.
func (m *BackgroundTaskManager) Wait(ctx context.Context, taskID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.validateBaseDeps(); err != nil {
		return err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()

	for {
		view, err := m.store.View(normalizedTaskID)
		if err != nil {
			return err
		}
		status := view.Runtime.Status
		if status.IsTerminal() {
			if status == TaskCompleted {
				return nil
			}
			reason := strings.TrimSpace(view.Runtime.FailureReason)
			if reason == "" {
				reason = fmt.Sprintf("task %q finished with status %q", normalizedTaskID, status)
			}
			return errors.New(reason)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("background manager: wait task %q: %w", normalizedTaskID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// KillTask marks one task as kill requested and cancels in-memory execution if running.
func (m *BackgroundTaskManager) KillTask(taskID string, reason string) error {
	if err := m.validateBaseDeps(); err != nil {
		return err
	}
	normalizedTaskID, err := normalizeTaskID(taskID)
	if err != nil {
		return err
	}

	return m.withTaskMutationLock(normalizedTaskID, func() error {
		control, err := m.store.ReadControl(normalizedTaskID)
		if err != nil {
			return err
		}
		now := nowUnixSeconds()
		control.KillRequestedAt = ptrFloat64(now)
		control.KillReason = strings.TrimSpace(reason)
		if control.KillReason == "" {
			control.KillReason = "killed by request"
		}
		if err := m.store.WriteControl(normalizedTaskID, control); err != nil {
			return err
		}

		task, running := m.getRunningTask(normalizedTaskID)
		if running && task.cancel != nil {
			task.cancel()
			return nil
		}

		rt, err := m.store.ReadRuntime(normalizedTaskID)
		if err != nil {
			return err
		}
		if rt.Status.IsTerminal() {
			return nil
		}

		rt.Status = TaskKilled
		rt.HeartbeatAt = ptrFloat64(now)
		rt.FinishedAt = ptrFloat64(now)
		rt.FailureReason = control.KillReason
		if err := m.store.WriteRuntime(normalizedTaskID, rt); err != nil {
			return err
		}
		return nil
	})
}

// Shutdown cancels all running tasks and waits until they exit.
func (m *BackgroundTaskManager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil {
		return errors.New("background manager: nil manager")
	}

	running := m.closeAndSnapshotRunning()
	for i := range running {
		if running[i].cancel != nil {
			running[i].cancel()
		}
	}

	waitCh := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("background manager: shutdown: %w", ctx.Err())
	}
}

func (m *BackgroundTaskManager) runBashTask(baseCtx context.Context, spec TaskSpec) {
	startedAt := nowUnixSeconds()
	if err := m.updateRuntime(spec.ID, func(rt *TaskRuntime) {
		rt.Status = TaskStarting
		rt.StartedAt = ptrFloat64(startedAt)
		rt.HeartbeatAt = ptrFloat64(startedAt)
		rt.FinishedAt = nil
		rt.ExitCode = nil
		rt.TimedOut = false
		rt.FailureReason = ""
	}); err != nil {
		return
	}

	runCtx, cancel := contextWithOptionalTimeout(baseCtx, spec.TimeoutSec)
	defer cancel()

	writer := newTaskOutputWriter(m.store, spec.ID)
	command := exec.CommandContext(runCtx, "bash", "-c", spec.Command)
	command.Stdout = writer
	command.Stderr = writer
	if spec.WorkDir != "" {
		command.Dir = spec.WorkDir
	}

	if err := command.Start(); err != nil {
		killRequested, killReason := m.killSnapshot(spec.ID)
		status, timedOut, failureReason := classifyTaskError(runCtx, err, spec.TimeoutSec, killRequested, killReason)
		_ = m.finalizeRuntime(spec.ID, runtimeFinalState{
			status:        status,
			timedOut:      timedOut,
			failureReason: failureReason,
		})
		return
	}

	_ = m.updateRuntime(spec.ID, func(rt *TaskRuntime) {
		now := nowUnixSeconds()
		rt.Status = TaskRunning
		rt.HeartbeatAt = ptrFloat64(now)
	})

	waitErr := command.Wait()
	if writerErr := writer.Err(); waitErr == nil && writerErr != nil {
		waitErr = writerErr
	}
	exitCode := commandExitCode(command)

	killRequested, killReason := m.killSnapshot(spec.ID)
	status, timedOut, failureReason := classifyTaskError(runCtx, waitErr, spec.TimeoutSec, killRequested, killReason)
	_ = m.finalizeRuntime(spec.ID, runtimeFinalState{
		status:        status,
		exitCode:      exitCode,
		timedOut:      timedOut,
		failureReason: failureReason,
	})
}

func (m *BackgroundTaskManager) runAgentTask(baseCtx context.Context, spec TaskSpec) {
	startedAt := nowUnixSeconds()
	if err := m.updateRuntime(spec.ID, func(rt *TaskRuntime) {
		rt.Status = TaskStarting
		rt.StartedAt = ptrFloat64(startedAt)
		rt.HeartbeatAt = ptrFloat64(startedAt)
		rt.FinishedAt = nil
		rt.ExitCode = nil
		rt.TimedOut = false
		rt.FailureReason = ""
	}); err != nil {
		return
	}

	runCtx, cancel := contextWithOptionalTimeout(baseCtx, spec.TimeoutSec)
	defer cancel()

	_ = m.updateRuntime(spec.ID, func(rt *TaskRuntime) {
		now := nowUnixSeconds()
		rt.Status = TaskRunning
		rt.HeartbeatAt = ptrFloat64(now)
	})

	result, runErr := m.subagentRunner.Run(runCtx, subagents.ForegroundRunRequest{
		AgentID:          spec.AgentID,
		SubagentType:     spec.SubagentType,
		Prompt:           spec.Prompt,
		ModelOverride:    spec.ModelOverride,
		Background:       true,
		BackgroundTaskID: spec.ID,
	})
	if output := extractAgentOutput(result); output != "" {
		if appendErr := m.store.AppendOutput(spec.ID, []byte(output+"\n")); appendErr != nil && runErr == nil {
			runErr = appendErr
		}
	}

	killRequested, killReason := m.killSnapshot(spec.ID)
	status, timedOut, failureReason := classifyTaskError(runCtx, runErr, spec.TimeoutSec, killRequested, killReason)
	var exitCode *int
	if runErr == nil {
		exitCode = ptrInt(0)
	}
	_ = m.finalizeRuntime(spec.ID, runtimeFinalState{
		status:        status,
		exitCode:      exitCode,
		timedOut:      timedOut,
		failureReason: failureReason,
	})
}

func (m *BackgroundTaskManager) validateBaseDeps() error {
	if m == nil {
		return errors.New("background manager: nil manager")
	}
	if m.store == nil {
		return errors.New("background manager: nil store")
	}
	return nil
}

func (m *BackgroundTaskManager) registerTask(taskID string) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, errors.New("background manager: manager is shut down")
	}
	if _, exists := m.tasks[taskID]; exists {
		return nil, fmt.Errorf("background manager: task %q already running", taskID)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	m.tasks[taskID] = runningTask{cancel: cancel}
	m.wg.Add(1)
	return runCtx, nil
}

func (m *BackgroundTaskManager) unregisterTask(taskID string) {
	m.mu.Lock()
	task, exists := m.tasks[taskID]
	if exists {
		delete(m.tasks, taskID)
	}
	m.mu.Unlock()

	if exists && task.cancel != nil {
		task.cancel()
	}
	m.wg.Done()
}

func (m *BackgroundTaskManager) getRunningTask(taskID string) (runningTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	return task, ok
}

func (m *BackgroundTaskManager) closeAndSnapshotRunning() []runningTask {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	running := make([]runningTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		running = append(running, task)
	}
	return running
}

func (m *BackgroundTaskManager) killSnapshot(taskID string) (bool, string) {
	control, err := m.store.ReadControl(taskID)
	if err != nil || control == nil || control.KillRequestedAt == nil {
		return false, ""
	}
	return true, strings.TrimSpace(control.KillReason)
}

func (m *BackgroundTaskManager) updateRuntime(taskID string, mutate func(rt *TaskRuntime)) error {
	return m.withTaskMutationLock(taskID, func() error {
		rt, err := m.store.ReadRuntime(taskID)
		if err != nil {
			return err
		}
		mutate(rt)
		return m.store.WriteRuntime(taskID, rt)
	})
}

func (m *BackgroundTaskManager) withTaskMutationLock(taskID string, do func() error) error {
	taskLock := m.taskMutationLock(taskID)
	taskLock.Lock()
	defer taskLock.Unlock()
	return do()
}

func (m *BackgroundTaskManager) taskMutationLock(taskID string) *sync.Mutex {
	lock, _ := m.runtimeLocks.LoadOrStore(taskID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

type runtimeFinalState struct {
	status        TaskStatus
	exitCode      *int
	timedOut      bool
	failureReason string
}

func (m *BackgroundTaskManager) finalizeRuntime(taskID string, final runtimeFinalState) error {
	return m.updateRuntime(taskID, func(rt *TaskRuntime) {
		now := nowUnixSeconds()
		rt.Status = final.status
		rt.HeartbeatAt = ptrFloat64(now)
		rt.FinishedAt = ptrFloat64(now)
		rt.ExitCode = cloneIntPtr(final.exitCode)
		rt.TimedOut = final.timedOut
		rt.FailureReason = strings.TrimSpace(final.failureReason)
		if final.status == TaskCompleted {
			rt.TimedOut = false
			rt.FailureReason = ""
		}
	})
}

func normalizeBashCreateSpec(spec TaskSpec) (TaskSpec, error) {
	normalized := spec
	normalized.ID = strings.TrimSpace(spec.ID)
	if normalized.ID == "" {
		normalized.ID = newTaskID()
	}

	normalizedTaskID, err := normalizeTaskID(normalized.ID)
	if err != nil {
		return TaskSpec{}, err
	}
	normalized.ID = normalizedTaskID
	normalized.Kind = TaskKindBash
	normalized.Command = strings.TrimSpace(spec.Command)
	normalized.WorkDir = strings.TrimSpace(spec.WorkDir)
	normalized.Description = strings.TrimSpace(spec.Description)
	normalized.ToolCallID = strings.TrimSpace(spec.ToolCallID)
	normalized.SessionID = strings.TrimSpace(spec.SessionID)
	normalized.TimeoutSec = spec.TimeoutSec
	if normalized.Command == "" {
		return TaskSpec{}, errors.New("background manager: command is required for bash task")
	}
	if normalized.TimeoutSec < 0 {
		return TaskSpec{}, errors.New("background manager: timeout_s must be >= 0")
	}
	return normalized, nil
}

func normalizeAgentCreateSpec(spec TaskSpec) (TaskSpec, error) {
	normalized := spec
	normalized.ID = strings.TrimSpace(spec.ID)
	if normalized.ID == "" {
		normalized.ID = newTaskID()
	}

	normalizedTaskID, err := normalizeTaskID(normalized.ID)
	if err != nil {
		return TaskSpec{}, err
	}
	normalized.ID = normalizedTaskID
	normalized.Kind = TaskKindAgent
	normalized.AgentID = strings.TrimSpace(spec.AgentID)
	normalized.SubagentType = strings.TrimSpace(spec.SubagentType)
	normalized.Prompt = strings.TrimSpace(spec.Prompt)
	normalized.ModelOverride = strings.TrimSpace(spec.ModelOverride)
	normalized.Description = strings.TrimSpace(spec.Description)
	normalized.ToolCallID = strings.TrimSpace(spec.ToolCallID)
	normalized.SessionID = strings.TrimSpace(spec.SessionID)
	normalized.TimeoutSec = spec.TimeoutSec
	if normalized.Prompt == "" {
		return TaskSpec{}, errors.New("background manager: prompt is required for agent task")
	}
	if normalized.TimeoutSec < 0 {
		return TaskSpec{}, errors.New("background manager: timeout_s must be >= 0")
	}
	return normalized, nil
}

func taskRecencyKey(view *TaskView) float64 {
	if view == nil {
		return 0
	}
	for _, ts := range []*float64{view.Runtime.FinishedAt, view.Runtime.HeartbeatAt, view.Runtime.StartedAt} {
		if ts != nil {
			return *ts
		}
	}
	return 0
}

func extractAgentOutput(value types.ToolReturnValue) string {
	if payload, ok := value.Value.(map[string]any); ok {
		if outputText, ok := payload["output_text"].(string); ok {
			return strings.TrimSpace(outputText)
		}
	}
	text := strings.TrimSpace(fmt.Sprint(value.Value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func classifyTaskError(
	runCtx context.Context,
	runErr error,
	timeoutSec int,
	killRequested bool,
	killReason string,
) (TaskStatus, bool, string) {
	if runErr == nil {
		return TaskCompleted, false, ""
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		reason := "task timed out"
		if timeoutSec > 0 {
			reason = fmt.Sprintf("task timed out after %d seconds", timeoutSec)
		}
		return TaskFailed, true, reason
	}

	if errors.Is(runCtx.Err(), context.Canceled) || killRequested {
		killReason = strings.TrimSpace(killReason)
		if killReason == "" {
			killReason = "task canceled"
		}
		return TaskKilled, false, killReason
	}
	return TaskFailed, false, strings.TrimSpace(runErr.Error())
}

func contextWithOptionalTimeout(parent context.Context, timeoutSec int) (context.Context, context.CancelFunc) {
	if timeoutSec <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
}

func newTaskID() string {
	sequence := atomic.AddUint64(&backgroundTaskSequence, 1)
	return fmt.Sprintf("task-%d-%d", time.Now().UTC().UnixNano(), sequence)
}

func commandExitCode(cmd *exec.Cmd) *int {
	if cmd == nil || cmd.ProcessState == nil {
		return nil
	}
	return ptrInt(cmd.ProcessState.ExitCode())
}

type taskOutputWriter struct {
	store  *BackgroundTaskStore
	taskID string

	mu  sync.Mutex
	err error
}

func newTaskOutputWriter(store *BackgroundTaskStore, taskID string) *taskOutputWriter {
	return &taskOutputWriter{store: store, taskID: taskID}
}

func (w *taskOutputWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}

	if err := w.store.AppendOutput(w.taskID, data); err != nil {
		w.err = err
		return 0, err
	}
	return len(data), nil
}

func (w *taskOutputWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func nowUnixSeconds() float64 {
	return float64(time.Now().UTC().UnixNano()) / 1e9
}

func ptrFloat64(v float64) *float64 {
	value := v
	return &value
}

func ptrInt(v int) *int {
	value := v
	return &value
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	return ptrInt(*v)
}
