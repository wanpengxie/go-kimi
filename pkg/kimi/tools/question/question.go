package question

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	toolparams "github.com/wanpengxie/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

const (
	toolName        = "ask_user_question"
	toolDescription = "Ask structured questions and wait for one user response over wire."

	defaultTimeoutSeconds = 120
	minTimeoutSeconds     = 1
	maxTimeoutSeconds     = 600
)

var (
	parameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt": {
      "type": "string",
      "description": "Optional top-level prompt shown before the questions"
    },
    "questions": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "header": {
            "type": "string",
            "description": "Optional short header"
          },
          "id": {
            "type": "string",
            "description": "Stable answer key id"
          },
          "question": {
            "type": "string",
            "description": "Question text"
          },
          "text": {
            "type": "string",
            "description": "Alias of question text"
          },
          "multi_select": {
            "type": "boolean",
            "description": "Whether this question allows multiple selections"
          },
          "options": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "label": {"type": "string"},
                "description": {"type": "string"},
                "value": {"type": "string"}
              },
              "required": ["label"],
              "additionalProperties": false
            }
          }
        },
        "required": ["id"],
        "additionalProperties": false
      }
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "default": 120,
      "description": "Maximum wait time for question_response"
    }
  },
  "required": ["questions"],
  "additionalProperties": false
}`)

	slugSanitizer     = regexp.MustCompile(`[^a-z0-9]+`)
	questionSequence  uint64
	defaultQuestionUI = "Please answer the following questions."
)

// YoloChecker returns current yolo mode.
type YoloChecker func() bool

// Tool implements ask_user_question.
type Tool struct {
	Hub            *wire.Hub
	Publisher      wire.Emitter
	TimeoutSeconds int
	IsYolo         YoloChecker
}

type executeParams struct {
	Prompt         string          `json:"prompt"`
	Questions      []questionInput `json:"questions"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

type questionInput struct {
	Header      string        `json:"header"`
	ID          string        `json:"id"`
	Question    string        `json:"question"`
	Text        string        `json:"text"`
	Options     []optionInput `json:"options"`
	MultiSelect bool          `json:"multi_select"`
}

type optionInput struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Value       string `json:"value"`
}

// New creates one ask_user_question tool.
func New(hub *wire.Hub, publisher wire.Emitter, isYolo YoloChecker) *Tool {
	return &Tool{
		Hub:       hub,
		Publisher: publisher,
		IsYolo:    isYolo,
	}
}

// Name returns the tool name.
func (*Tool) Name() string {
	return toolName
}

// Description returns the tool description.
func (*Tool) Description() string {
	return toolDescription
}

// ParameterSchema returns schema for tool parameters.
func (*Tool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(parameterSchema)
}

// Execute sends one question_request and waits for matching question_response.
func (t *Tool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}
	if t == nil || t.Hub == nil {
		return errorResult("ask_user_question: wire hub is not configured"), nil
	}

	publisher := t.publisher()
	if publisher == nil {
		return errorResult("ask_user_question: wire publisher is not configured"), nil
	}

	if t.isYolo() {
		return successResult(map[string]any{
			"request_id": newQuestionRequestID(),
			"dismissed":  true,
			"reason":     "yolo_mode",
			"answers":    map[string]string{},
		}), nil
	}

	request := buildQuestionRequest(input)
	subscriber := t.Hub.Subscribe()
	defer t.Hub.Unsubscribe(subscriber)

	if emitErr := publisher.Emit(request); emitErr != nil {
		return errorResult(fmt.Sprintf("ask_user_question: publish question request: %v", emitErr)), nil
	}

	timeout := t.resolveTimeout(input.TimeoutSeconds)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var yoloTicker *time.Ticker
	var yoloTick <-chan time.Time
	if t != nil && t.IsYolo != nil {
		yoloTicker = time.NewTicker(100 * time.Millisecond)
		yoloTick = yoloTicker.C
		defer yoloTicker.Stop()
	}

	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return errorResult(fmt.Sprintf("ask_user_question: wait response timed out after %d seconds", int(timeout.Seconds()))), nil
			}
			return errorResult(fmt.Sprintf("ask_user_question: wait response canceled: %v", waitCtx.Err())), nil
		case <-yoloTick:
			if t.isYolo() {
				return successResult(map[string]any{
					"request_id": request.ID,
					"dismissed":  true,
					"reason":     "yolo_mode",
					"answers":    map[string]string{},
				}), nil
			}
		case message, ok := <-subscriber:
			if !ok {
				return errorResult("ask_user_question: wire subscriber closed"), nil
			}
			response, ok := message.(wire.QuestionResponse)
			if !ok {
				continue
			}
			if strings.TrimSpace(response.RequestID) != request.ID {
				continue
			}
			answers := cloneAnswers(response.Answers)
			dismissed := len(answers) == 0
			payload := map[string]any{
				"request_id":   request.ID,
				"dismissed":    dismissed,
				"answers":      answers,
				"submitted_at": strings.TrimSpace(response.SubmittedAt),
			}
			if dismissed {
				payload["reason"] = "dismissed"
			}
			return successResult(payload), nil
		}
	}
}

func decodeParams(raw json.RawMessage) (executeParams, error) {
	input := executeParams{
		TimeoutSeconds: defaultTimeoutSeconds,
	}
	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return executeParams{}, fmt.Errorf("ask_user_question: decode params: %w", err)
	}

	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		input.Prompt = defaultQuestionUI
	}
	if input.TimeoutSeconds == 0 {
		input.TimeoutSeconds = defaultTimeoutSeconds
	}
	if input.TimeoutSeconds < minTimeoutSeconds || input.TimeoutSeconds > maxTimeoutSeconds {
		return executeParams{}, fmt.Errorf(
			"ask_user_question: timeout_seconds must be in range [%d, %d]",
			minTimeoutSeconds,
			maxTimeoutSeconds,
		)
	}
	if len(input.Questions) == 0 {
		return executeParams{}, errors.New("ask_user_question: questions is required")
	}

	seenIDs := map[string]struct{}{}
	for i := range input.Questions {
		item := &input.Questions[i]
		item.Header = strings.TrimSpace(item.Header)
		item.ID = strings.TrimSpace(item.ID)
		item.Question = strings.TrimSpace(item.Question)
		item.Text = strings.TrimSpace(item.Text)
		if item.Question == "" {
			item.Question = item.Text
		}

		if item.ID == "" {
			return executeParams{}, fmt.Errorf("ask_user_question: questions[%d].id is required", i)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return executeParams{}, fmt.Errorf("ask_user_question: questions[%d].id duplicates %q", i, item.ID)
		}
		seenIDs[item.ID] = struct{}{}

		if item.Question == "" {
			return executeParams{}, fmt.Errorf("ask_user_question: questions[%d].question is required", i)
		}

		for j := range item.Options {
			option := &item.Options[j]
			option.Label = strings.TrimSpace(option.Label)
			option.Description = strings.TrimSpace(option.Description)
			option.Value = strings.TrimSpace(option.Value)
			if option.Label == "" {
				return executeParams{}, fmt.Errorf("ask_user_question: questions[%d].options[%d].label is required", i, j)
			}
			if option.Value == "" {
				option.Value = defaultOptionValue(option.Label, j)
			}
		}
	}

	return input, nil
}

func buildQuestionRequest(input executeParams) wire.QuestionRequest {
	items := make([]wire.QuestionItem, 0, len(input.Questions))
	allowMultiple := false
	for i := range input.Questions {
		question := input.Questions[i]
		if question.MultiSelect {
			allowMultiple = true
		}

		options := make([]wire.QuestionOption, 0, len(question.Options))
		for j := range question.Options {
			option := question.Options[j]
			options = append(options, wire.QuestionOption{
				Label:       option.Label,
				Description: option.Description,
				Value:       option.Value,
			})
		}

		items = append(items, wire.QuestionItem{
			Header:   question.Header,
			ID:       question.ID,
			Question: question.Question,
			Options:  options,
		})
	}

	return wire.QuestionRequest{
		ID:            newQuestionRequestID(),
		Prompt:        input.Prompt,
		Items:         items,
		AllowMultiple: allowMultiple,
	}
}

func newQuestionRequestID() string {
	sequence := atomic.AddUint64(&questionSequence, 1)
	return fmt.Sprintf("question-%d-%d", time.Now().UTC().UnixNano(), sequence)
}

func defaultOptionValue(label string, index int) string {
	normalized := strings.ToLower(strings.TrimSpace(label))
	if normalized == "" {
		return fmt.Sprintf("option_%d", index+1)
	}
	normalized = slugSanitizer.ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "_")
	if normalized == "" {
		return fmt.Sprintf("option_%d", index+1)
	}
	return normalized
}

func (t *Tool) resolveTimeout(inputSeconds int) time.Duration {
	if inputSeconds > 0 {
		return time.Duration(inputSeconds) * time.Second
	}
	if t != nil && t.TimeoutSeconds > 0 {
		return time.Duration(t.TimeoutSeconds) * time.Second
	}
	return time.Duration(defaultTimeoutSeconds) * time.Second
}

func (t *Tool) isYolo() bool {
	if t == nil || t.IsYolo == nil {
		return false
	}
	return t.IsYolo()
}

func (t *Tool) publisher() wire.Emitter {
	if t == nil {
		return nil
	}
	if t.Publisher != nil {
		return t.Publisher
	}
	return t.Hub
}

func successResult(payload map[string]any) types.ToolResult {
	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: payload,
		},
	}
}

func errorResult(message string) types.ToolResult {
	return types.ToolResult{
		Name:    toolName,
		IsError: true,
		Value: types.ToolReturnValue{
			Value: strings.TrimSpace(message),
		},
	}
}

func cloneAnswers(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
