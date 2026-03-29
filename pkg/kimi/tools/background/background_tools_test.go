package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
)

func TestTaskOutputExecuteSuccess(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task:   sampleTaskView("task-1", corebg.TaskRunning),
		output: []byte("first line\nsecond line"),
	}
	tool := NewTaskOutput(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_output) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_output) IsError = true, result=%#v", result)
	}

	if len(manager.getCalls) != 1 || manager.getCalls[0] != "task-1" {
		t.Fatalf("manager GetTask calls = %#v, want [task-1]", manager.getCalls)
	}
	if len(manager.tailCalls) != 1 {
		t.Fatalf("manager TailOutput call count = %d, want 1", len(manager.tailCalls))
	}
	if manager.tailCalls[0].maxBytes != defaultTaskOutputMaxBytes {
		t.Fatalf("TailOutput maxBytes = %d, want %d", manager.tailCalls[0].maxBytes, defaultTaskOutputMaxBytes)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["output"].(string); !strings.Contains(got, "first line") {
		t.Fatalf("output = %q, want contains first line", got)
	}
	taskPayload, ok := payload["task"].(map[string]any)
	if !ok {
		t.Fatalf("task payload type = %T, want map[string]any", payload["task"])
	}
	if got, _ := taskPayload["task_id"].(string); got != "task-1" {
		t.Fatalf("task.task_id = %q, want %q", got, "task-1")
	}
}

func TestTaskOutputExecuteReadError(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task:    sampleTaskView("task-1", corebg.TaskRunning),
		tailErr: errors.New("read failed"),
	}
	tool := NewTaskOutput(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_output read error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(task_output read error) IsError = false, want true")
	}
}

func TestTaskOutputExecuteAfterCompletion(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task: sampleTaskView("task-1", corebg.TaskCompleted),
		tailChunk: &corebg.TaskOutputChunk{
			TaskID:     "task-1",
			Status:     corebg.TaskCompleted,
			Offset:     0,
			NextOffset: 10,
			Output:     "final logs",
			EOF:        true,
		},
	}
	tool := NewTaskOutput(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_output completed) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_output completed) IsError = true, result=%#v", result)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["status"].(string); got != string(corebg.TaskCompleted) {
		t.Fatalf("payload.status = %q, want %q", got, corebg.TaskCompleted)
	}
	if eof, _ := payload["eof"].(bool); !eof {
		t.Fatalf("payload.eof = %v, want true", eof)
	}
}

func TestTaskOutputExecuteMissingTaskDoesNotReadOutput(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{getErr: errors.New("task not found")}
	tool := NewTaskOutput(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-missing"}`))
	if err != nil {
		t.Fatalf("Execute(task_output missing) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(task_output missing) IsError = false, want true")
	}
	if len(manager.tailCalls) != 0 {
		t.Fatalf("TailOutput call count = %d, want 0", len(manager.tailCalls))
	}
	if len(manager.consumerCalls) != 0 {
		t.Fatalf("ReadConsumerOutput call count = %d, want 0", len(manager.consumerCalls))
	}
}

func TestTaskOutputExecuteWithConsumerID(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task:   sampleTaskView("task-1", corebg.TaskRunning),
		output: []byte("consumer output"),
	}
	tool := NewTaskOutput(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1","consumer_id":"ui-main"}`))
	if err != nil {
		t.Fatalf("Execute(task_output consumer) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_output consumer) IsError = true, result=%#v", result)
	}
	if len(manager.consumerCalls) != 1 {
		t.Fatalf("ReadConsumerOutput call count = %d, want 1", len(manager.consumerCalls))
	}
	if len(manager.tailCalls) != 0 {
		t.Fatalf("TailOutput call count = %d, want 0", len(manager.tailCalls))
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	chunk, ok := payload["chunk"].(map[string]any)
	if !ok {
		t.Fatalf("chunk payload type = %T, want map[string]any", payload["chunk"])
	}
	if got, _ := chunk["consumer_id"].(string); got != "ui-main" {
		t.Fatalf("chunk.consumer_id = %q, want ui-main", got)
	}
}

func TestTaskOutputDecodeValidation(t *testing.T) {
	t.Parallel()

	tool := NewTaskOutput(&fakeTaskManager{})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("Execute(task_output missing task_id) error = nil, want error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"t","offset":-1}`)); err == nil {
		t.Fatal("Execute(task_output negative offset) error = nil, want error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"task_id":"t","max_bytes":%d}`, corebg.MaxTaskOutputBytes+1))); err == nil {
		t.Fatal("Execute(task_output oversized max_bytes) error = nil, want error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"t","unexpected":true}`)); err == nil {
		t.Fatal("Execute(task_output unexpected field) error = nil, want error")
	} else if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Execute(task_output unexpected field) error = %q, want contains unknown field", err.Error())
	}
}

func TestTaskListExecuteSuccess(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		tasks: []*corebg.TaskView{
			sampleTaskView("task-1", corebg.TaskCompleted),
			sampleTaskView("task-2", corebg.TaskRunning),
		},
	}
	tool := NewTaskList(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute(task_list) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_list) IsError = true, result=%#v", result)
	}

	if len(manager.listCalls) != 1 || manager.listCalls[0] != defaultTaskListLimit {
		t.Fatalf("manager ListTasks calls = %#v, want [%d]", manager.listCalls, defaultTaskListLimit)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["count"].(int); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	tasks, ok := payload["tasks"].([]map[string]any)
	if !ok {
		t.Fatalf("tasks payload type = %T, want []map[string]any", payload["tasks"])
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
}

func TestTaskListExecuteManagerError(t *testing.T) {
	t.Parallel()

	tool := NewTaskList(&fakeTaskManager{listErr: errors.New("list failed")})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"limit":3}`))
	if err != nil {
		t.Fatalf("Execute(task_list manager error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(task_list manager error) IsError = false, want true")
	}
}

func TestTaskListDecodeValidation(t *testing.T) {
	t.Parallel()

	tool := NewTaskList(&fakeTaskManager{})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"limit":-1}`)); err == nil {
		t.Fatal("Execute(task_list negative limit) error = nil, want error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"limit":201}`)); err == nil {
		t.Fatal("Execute(task_list too large limit) error = nil, want error")
	}
}

func TestTaskStopExecuteSuccess(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task: sampleTaskView("task-1", corebg.TaskKilled),
	}
	tool := NewTaskStop(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1","reason":"manual stop"}`))
	if err != nil {
		t.Fatalf("Execute(task_stop) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_stop) IsError = true, result=%#v", result)
	}

	if len(manager.killCalls) != 1 {
		t.Fatalf("KillTask call count = %d, want 1", len(manager.killCalls))
	}
	if manager.killCalls[0].taskID != "task-1" || manager.killCalls[0].reason != "manual stop" {
		t.Fatalf("KillTask call = %#v, want task-1/manual stop", manager.killCalls[0])
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["message"].(string); got != "kill requested" {
		t.Fatalf("message = %q, want kill requested", got)
	}
}

func TestTaskStopExecuteGetFallback(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		getErr: errors.New("not found after kill"),
	}
	tool := NewTaskStop(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_stop fallback) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_stop fallback) IsError = true, result=%#v", result)
	}
	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	if got, _ := payload["status"].(string); got != "kill_requested" {
		t.Fatalf("status = %q, want kill_requested", got)
	}
}

func TestTaskStopExecuteKillError(t *testing.T) {
	t.Parallel()

	tool := NewTaskStop(&fakeTaskManager{killErr: errors.New("kill failed")})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_stop kill error) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("Execute(task_stop kill error) IsError = false, want true")
	}
}

func TestTaskStopExecuteTerminalTaskNoop(t *testing.T) {
	t.Parallel()

	manager := &fakeTaskManager{
		task: sampleTaskView("task-1", corebg.TaskCompleted),
	}
	tool := NewTaskStop(manager)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("Execute(task_stop terminal) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(task_stop terminal) IsError = true, result=%#v", result)
	}
	if len(manager.killCalls) != 1 {
		t.Fatalf("KillTask call count = %d, want 1", len(manager.killCalls))
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	taskPayload, ok := payload["task"].(map[string]any)
	if !ok {
		t.Fatalf("task payload type = %T, want map[string]any", payload["task"])
	}
	if got, _ := taskPayload["status"].(string); got != string(corebg.TaskCompleted) {
		t.Fatalf("task.status = %q, want %q", got, corebg.TaskCompleted)
	}
}

func TestTaskStopDecodeValidation(t *testing.T) {
	t.Parallel()

	tool := NewTaskStop(&fakeTaskManager{})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("Execute(task_stop missing task_id) error = nil, want error")
	}
}

type fakeTaskManager struct {
	task   *corebg.TaskView
	tasks  []*corebg.TaskView
	output []byte

	tailChunk     *corebg.TaskOutputChunk
	consumerChunk *corebg.TaskOutputChunk

	getErr      error
	listErr     error
	readErr     error
	tailErr     error
	consumerErr error
	killErr     error

	getCalls      []string
	listCalls     []int
	readCalls     []readCall
	tailCalls     []readCall
	consumerCalls []consumerReadCall
	killCalls     []killCall
}

type readCall struct {
	taskID   string
	offset   int64
	maxBytes int
}

type killCall struct {
	taskID string
	reason string
}

type consumerReadCall struct {
	taskID     string
	consumerID string
	maxBytes   int
}

func (m *fakeTaskManager) GetTask(taskID string) (*corebg.TaskView, error) {
	m.getCalls = append(m.getCalls, taskID)
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.task == nil {
		return nil, errors.New("task not found")
	}
	return m.task, nil
}

func (m *fakeTaskManager) ListTasks(limit int) ([]*corebg.TaskView, error) {
	m.listCalls = append(m.listCalls, limit)
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.tasks, nil
}

func (m *fakeTaskManager) ReadOutput(taskID string, offset int64, maxBytes int) ([]byte, error) {
	m.readCalls = append(m.readCalls, readCall{
		taskID:   taskID,
		offset:   offset,
		maxBytes: maxBytes,
	})
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.output, nil
}

func (m *fakeTaskManager) TailOutput(taskID string, offset int64, maxBytes int) (corebg.TaskOutputChunk, error) {
	m.tailCalls = append(m.tailCalls, readCall{
		taskID:   taskID,
		offset:   offset,
		maxBytes: maxBytes,
	})
	if m.tailErr != nil {
		return corebg.TaskOutputChunk{}, m.tailErr
	}
	if m.tailChunk != nil {
		chunk := *m.tailChunk
		if chunk.TaskID == "" {
			chunk.TaskID = taskID
		}
		return chunk, nil
	}
	status := corebg.TaskRunning
	if m.task != nil {
		status = m.task.Runtime.Status
	}
	return corebg.TaskOutputChunk{
		TaskID:     taskID,
		Status:     status,
		Offset:     offset,
		NextOffset: offset + int64(len(m.output)),
		Output:     string(m.output),
		EOF:        false,
	}, nil
}

func (m *fakeTaskManager) ReadConsumerOutput(taskID string, consumerID string, maxBytes int) (corebg.TaskOutputChunk, error) {
	m.consumerCalls = append(m.consumerCalls, consumerReadCall{
		taskID:     taskID,
		consumerID: consumerID,
		maxBytes:   maxBytes,
	})
	if m.consumerErr != nil {
		return corebg.TaskOutputChunk{}, m.consumerErr
	}
	if m.consumerChunk != nil {
		chunk := *m.consumerChunk
		if chunk.TaskID == "" {
			chunk.TaskID = taskID
		}
		if chunk.ConsumerID == "" {
			chunk.ConsumerID = consumerID
		}
		return chunk, nil
	}
	status := corebg.TaskRunning
	if m.task != nil {
		status = m.task.Runtime.Status
	}
	return corebg.TaskOutputChunk{
		TaskID:     taskID,
		ConsumerID: consumerID,
		Status:     status,
		Offset:     0,
		NextOffset: int64(len(m.output)),
		Output:     string(m.output),
		EOF:        false,
	}, nil
}

func (m *fakeTaskManager) KillTask(taskID string, reason string) error {
	m.killCalls = append(m.killCalls, killCall{taskID: taskID, reason: reason})
	if m.killErr != nil {
		return m.killErr
	}
	return nil
}

func sampleTaskView(taskID string, status corebg.TaskStatus) *corebg.TaskView {
	startedAt := 10.0
	return &corebg.TaskView{
		Spec: corebg.TaskSpec{
			ID:          taskID,
			Kind:        corebg.TaskKindAgent,
			Description: "sample task",
			SessionID:   "session-1",
		},
		Runtime: corebg.TaskRuntime{
			Status:    status,
			StartedAt: &startedAt,
		},
	}
}
