package question

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestAskUserQuestionExecuteWaitsForMatchingResponse(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(16)
	tool := New(hub, hub, func() bool { return false })
	tool.TimeoutSeconds = 2

	observer := hub.Subscribe()
	defer hub.Unsubscribe(observer)

	type executeResult struct {
		result types.ToolResult
		err    error
	}
	resultCh := make(chan executeResult, 1)
	go func() {
		result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
			"prompt": "choose rollout",
			"questions": []map[string]any{
				{
					"id":       "strategy",
					"question": "which strategy",
					"options": []map[string]any{
						{"label": "Blue/Green"},
						{"label": "Rolling"},
					},
				},
			},
		}))
		resultCh <- executeResult{result: result, err: err}
	}()

	request := mustReadQuestionRequest(t, observer)
	hub.Publish(wire.QuestionResponse{
		RequestID: request.ID,
		Answers: map[string]string{
			"strategy": "blue_green",
		},
		SubmittedAt: "2026-03-28T23:40:00Z",
	})

	executed := <-resultCh
	if executed.err != nil {
		t.Fatalf("Execute() error = %v", executed.err)
	}
	if executed.result.IsError {
		t.Fatalf("result.IsError = %v, want false", executed.result.IsError)
	}

	payload := resultPayload(t, executed.result)
	if dismissed, _ := payload["dismissed"].(bool); dismissed {
		t.Fatalf("payload.dismissed = %v, want false", dismissed)
	}
	answers, _ := payload["answers"].(map[string]string)
	if len(answers) == 0 {
		rawAnswers, _ := payload["answers"].(map[string]any)
		if got, _ := rawAnswers["strategy"].(string); got != "blue_green" {
			t.Fatalf("payload.answers.strategy = %q, want %q", got, "blue_green")
		}
	} else if got := answers["strategy"]; got != "blue_green" {
		t.Fatalf("payload.answers.strategy = %q, want %q", got, "blue_green")
	}
}

func TestAskUserQuestionExecuteYoloDismissesImmediately(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(8)
	tool := New(hub, hub, func() bool { return true })

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"questions": []map[string]any{
			{
				"id":   "approval",
				"text": "ship now?",
			},
		},
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	payload := resultPayload(t, result)
	if dismissed, _ := payload["dismissed"].(bool); !dismissed {
		t.Fatalf("payload.dismissed = %v, want true", dismissed)
	}
	if reason, _ := payload["reason"].(string); reason != "yolo_mode" {
		t.Fatalf("payload.reason = %q, want %q", reason, "yolo_mode")
	}
}

func TestAskUserQuestionExecuteTimeoutReturnsToolError(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(8)
	tool := New(hub, hub, func() bool { return false })
	tool.TimeoutSeconds = 1

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"timeout_seconds": 1,
		"questions": []map[string]any{
			{
				"id":       "deployment",
				"question": "continue deployment?",
			},
		},
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	output, _ := result.Value.Value.(string)
	if !strings.Contains(output, "timed out") {
		t.Fatalf("result output = %q, want contains timed out", output)
	}
}

func TestAskUserQuestionExecuteValidatesInput(t *testing.T) {
	t.Parallel()

	tool := New(wire.NewHub(4), nil, nil)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[]}`)); err == nil {
		t.Fatal("Execute(empty questions) error = nil, want validation error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[{"id":"a"}]}`)); err == nil {
		t.Fatal("Execute(missing question text) error = nil, want validation error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[{"id":"a","question":"x"},{"id":"a","question":"y"}]}`)); err == nil {
		t.Fatal("Execute(duplicate id) error = nil, want validation error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[{"id":"a","question":"x"}],"unexpected":true}`)); err == nil {
		t.Fatal("Execute(unexpected field) error = nil, want validation error")
	}
}

func mustReadQuestionRequest(t *testing.T, ch <-chan wire.WireMessage) wire.QuestionRequest {
	t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting question request")
		case message, ok := <-ch:
			if !ok {
				t.Fatal("question observer channel closed unexpectedly")
			}
			request, ok := message.(wire.QuestionRequest)
			if !ok {
				continue
			}
			return request
		}
	}
}

func mustParams(t *testing.T, input any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func resultPayload(t *testing.T, result types.ToolResult) map[string]any {
	t.Helper()
	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	return payload
}
