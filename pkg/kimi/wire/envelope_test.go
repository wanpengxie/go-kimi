package wire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

var (
	_ Event       = TurnBegin{}
	_ Event       = TextDelta{}
	_ Event       = SteerInput{}
	_ Event       = TurnEnd{}
	_ Event       = StepBegin{}
	_ Event       = StepInterrupted{}
	_ Event       = CompactionBegin{}
	_ Event       = CompactionEnd{}
	_ Event       = MCPLoadingBegin{}
	_ Event       = MCPLoadingEnd{}
	_ Event       = StatusUpdate{}
	_ Event       = Notification{}
	_ Event       = SubagentEvent{}
	_ Request     = ApprovalRequest{}
	_ Request     = ApprovalResponse{}
	_ Request     = ToolCallRequest{}
	_ Event       = ToolCallResult{}
	_ Request     = QuestionRequest{}
	_ Request     = QuestionResponse{}
	_ Request     = QuestionOption{}
	_ Request     = QuestionItem{}
	_ WireMessage = TurnBegin{}
	_ WireMessage = ApprovalRequest{}
)

type unregisteredWireMessage struct{}

func (unregisteredWireMessage) wireMessageType() WireMessageType {
	return "unregistered"
}

func TestSerializeDeserializeWireMessageRoundTripAllTypes(t *testing.T) {
	t.Parallel()

	snapshot := &MCPServerSnapshot{
		Servers: []MCPStatusSnapshot{
			{Name: "filesystem", Status: "ready"},
			{Name: "search", Status: "loading", Disabled: true, Message: "disabled by config"},
		},
	}

	cases := []struct {
		name     string
		message  WireMessage
		wantType WireMessageType
	}{
		{
			name: "turn_begin",
			message: TurnBegin{
				TurnID: "turn-1",
				Input: types.ContentParts{
					types.TextPart{Text: "hello"},
					types.ThinkPart{Think: "trace"},
				},
			},
			wantType: WireMessageTypeTurnBegin,
		},
		{
			name: "text_delta",
			message: TextDelta{
				TurnID: "turn-1",
				Delta:  "hel",
			},
			wantType: WireMessageTypeTextDelta,
		},
		{
			name: "steer_input",
			message: SteerInput{
				Text:     "focus on tests",
				Priority: "high",
			},
			wantType: WireMessageTypeSteerInput,
		},
		{
			name: "turn_end",
			message: TurnEnd{
				TurnID:     "turn-1",
				StopReason: "stop",
				Output: types.ContentParts{
					types.TextPart{Text: "done"},
				},
				Usage: &types.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
			},
			wantType: WireMessageTypeTurnEnd,
		},
		{
			name: "step_begin",
			message: StepBegin{
				StepID:      "step-1",
				Name:        "plan",
				Description: "generate a plan",
			},
			wantType: WireMessageTypeStepBegin,
		},
		{
			name: "step_interrupted",
			message: StepInterrupted{
				StepID: "step-1",
				Reason: "user stop",
			},
			wantType: WireMessageTypeStepInterrupted,
		},
		{
			name: "compaction_begin",
			message: CompactionBegin{
				Trigger: "token_limit",
			},
			wantType: WireMessageTypeCompactionBegin,
		},
		{
			name: "compaction_end",
			message: CompactionEnd{
				Summary: "compacted context",
				Content: types.ContentParts{
					types.TextPart{Text: "summary"},
				},
			},
			wantType: WireMessageTypeCompactionEnd,
		},
		{
			name: "mcp_loading_begin",
			message: MCPLoadingBegin{
				Snapshot: snapshot,
			},
			wantType: WireMessageTypeMCPLoadingBegin,
		},
		{
			name: "mcp_loading_end",
			message: MCPLoadingEnd{
				Snapshot:   snapshot,
				DurationMS: 1200,
			},
			wantType: WireMessageTypeMCPLoadingEnd,
		},
		{
			name: "status_update",
			message: StatusUpdate{
				Status:  "running",
				Message: "executing",
			},
			wantType: WireMessageTypeStatusUpdate,
		},
		{
			name: "notification",
			message: Notification{
				Level:   "info",
				Message: "hello",
				Blocks: types.DisplayBlocks{
					types.BriefDisplayBlock{Text: "hello"},
				},
			},
			wantType: WireMessageTypeNotification,
		},
		{
			name: "subagent_event",
			message: SubagentEvent{
				AgentID:   "agent-1",
				EventType: "started",
				Message:   "subagent started",
				Payload: map[string]any{
					"attempt": 1,
				},
			},
			wantType: WireMessageTypeSubagentEvent,
		},
		{
			name: "approval_request",
			message: ApprovalRequest{
				ID:          "approval-1",
				Kind:        "shell",
				Title:       "Run command",
				Description: "Need permission to run shell command",
				Command:     "rm -rf /tmp/cache",
				Arguments:   []string{"-f"},
				Metadata: map[string]any{
					"risk": "high",
				},
			},
			wantType: WireMessageTypeApprovalRequest,
		},
		{
			name: "approval_response",
			message: ApprovalResponse{
				RequestID: "approval-1",
				Approved:  true,
				Reason:    "confirmed",
			},
			wantType: WireMessageTypeApprovalResponse,
		},
		{
			name: "tool_call_request",
			message: ToolCallRequest{
				ID: "tool-req-1",
				ToolCall: types.ToolCall{
					ID:   "call-1",
					Name: "search",
					Arguments: map[string]any{
						"query": "go generics",
					},
				},
			},
			wantType: WireMessageTypeToolCallRequest,
		},
		{
			name: "tool_call_result",
			message: ToolCallResult{
				ID: "tool-req-1",
				Result: types.ToolResult{
					ToolCallID: "call-1",
					Name:       "search",
					Value: types.ToolReturnValue{
						Value: map[string]any{
							"items": []any{"a", "b"},
						},
					},
				},
			},
			wantType: WireMessageTypeToolCallResult,
		},
		{
			name: "question_request",
			message: QuestionRequest{
				ID:     "question-1",
				Prompt: "choose deployment strategy",
				Items: []QuestionItem{
					{
						Header:   "deploy",
						ID:       "strategy",
						Question: "which strategy",
						Options: []QuestionOption{
							{Label: "Blue/Green", Value: "bg", Description: "near zero downtime"},
							{Label: "Rolling", Value: "rolling"},
						},
					},
				},
				AllowMultiple: false,
			},
			wantType: WireMessageTypeQuestionRequest,
		},
		{
			name: "question_response",
			message: QuestionResponse{
				RequestID: "question-1",
				Answers: map[string]string{
					"strategy": "bg",
				},
				SubmittedAt: "2026-03-27T08:00:00Z",
			},
			wantType: WireMessageTypeQuestionResponse,
		},
		{
			name: "question_option",
			message: QuestionOption{
				Label:       "Option A",
				Description: "first option",
				Value:       "a",
			},
			wantType: WireMessageTypeQuestionOption,
		},
		{
			name: "question_item",
			message: QuestionItem{
				Header:   "header",
				ID:       "item-1",
				Question: "what next",
				Options: []QuestionOption{
					{Label: "Continue", Value: "continue"},
				},
			},
			wantType: WireMessageTypeQuestionItem,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			envelope, err := SerializeWireMessage(tc.message)
			if err != nil {
				t.Fatalf("SerializeWireMessage() error = %v", err)
			}
			if envelope.Type != tc.wantType {
				t.Fatalf("envelope.Type = %q, want %q", envelope.Type, tc.wantType)
			}

			var data map[string]any
			rawEnvelope, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("json.Marshal(envelope) error = %v", err)
			}
			if err := json.Unmarshal(rawEnvelope, &data); err != nil {
				t.Fatalf("json.Unmarshal(envelope map) error = %v", err)
			}

			decoded, err := DeserializeWireMessage(data)
			if err != nil {
				t.Fatalf("DeserializeWireMessage() error = %v", err)
			}

			assertSameMessage(t, decoded, tc.message)
		})
	}
}

func TestWireMessageSerdeErrors(t *testing.T) {
	t.Parallel()

	if _, err := SerializeWireMessage(nil); err == nil {
		t.Fatal("SerializeWireMessage(nil) expected error")
	}
	var nilTurnBegin *TurnBegin
	if _, err := SerializeWireMessage(nilTurnBegin); err == nil {
		t.Fatal("SerializeWireMessage(typed nil pointer) expected error")
	}
	if _, err := SerializeWireMessage(unregisteredWireMessage{}); err == nil || !strings.Contains(err.Error(), "unregistered type") {
		t.Fatalf("SerializeWireMessage(unregistered) error = %v", err)
	}

	if _, err := DeserializeWireMessage(nil); err == nil {
		t.Fatal("DeserializeWireMessage(nil) expected error")
	}

	_, err := DeserializeWireMessage(map[string]any{"payload": map[string]any{"turn_id": "turn-1"}})
	if err == nil || !strings.Contains(err.Error(), "missing type") {
		t.Fatalf("DeserializeWireMessage missing type error = %v", err)
	}

	_, err = DeserializeWireMessage(map[string]any{"type": "unknown", "payload": map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("DeserializeWireMessage unknown type error = %v", err)
	}

	_, err = DeserializeWireMessage(map[string]any{"type": "turn_begin"})
	if err == nil || !strings.Contains(err.Error(), "missing payload") {
		t.Fatalf("DeserializeWireMessage missing payload error = %v", err)
	}

	_, err = DeserializeWireMessage(map[string]any{"type": "turn_begin", "payload": "bad"})
	if err == nil {
		t.Fatal("DeserializeWireMessage invalid payload expected error")
	}
}

func TestWireMessageRegistryCompleteness(t *testing.T) {
	t.Parallel()

	expected := []WireMessageType{
		WireMessageTypeTurnBegin,
		WireMessageTypeTextDelta,
		WireMessageTypeSteerInput,
		WireMessageTypeTurnEnd,
		WireMessageTypeStepBegin,
		WireMessageTypeStepInterrupted,
		WireMessageTypeCompactionBegin,
		WireMessageTypeCompactionEnd,
		WireMessageTypeMCPLoadingBegin,
		WireMessageTypeMCPLoadingEnd,
		WireMessageTypeStatusUpdate,
		WireMessageTypeNotification,
		WireMessageTypeSubagentEvent,
		WireMessageTypeApprovalRequest,
		WireMessageTypeApprovalResponse,
		WireMessageTypeToolCallRequest,
		WireMessageTypeToolCallResult,
		WireMessageTypeQuestionRequest,
		WireMessageTypeQuestionResponse,
		WireMessageTypeQuestionOption,
		WireMessageTypeQuestionItem,
	}

	if len(wireMessageDecoders) != len(expected) {
		t.Fatalf("registry size = %d, want %d", len(wireMessageDecoders), len(expected))
	}

	for _, typ := range expected {
		if _, ok := wireMessageDecoders[typ]; !ok {
			t.Fatalf("missing decoder registration for %q", typ)
		}
	}
}

func assertSameMessage(t *testing.T, got WireMessage, want WireMessage) {
	t.Helper()

	if reflect.TypeOf(got) != reflect.TypeOf(want) {
		t.Fatalf("message type = %T, want %T", got, want)
	}

	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(got) error = %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal(want) error = %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("message json mismatch\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}
