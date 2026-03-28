package dmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	toolparams "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	toolName        = "send_dmail"
	toolDescription = "Revert context to one checkpoint and append one follow-up mail message."
)

var parameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "checkpoint_id": {
      "type": "integer",
      "minimum": 0,
      "description": "Context checkpoint id to revert to before sending mail"
    },
    "message": {
      "type": "string",
      "description": "Mail message content appended into context"
    }
  },
  "required": ["checkpoint_id", "message"],
  "additionalProperties": false
}`)

// MailContext defines the context operations required by send_dmail.
type MailContext interface {
	RevertTo(checkpointID int) error
	Append(m soul.Message) error
}

// Tool implements send_dmail.
type Tool struct {
	Context MailContext
}

type executeParams struct {
	CheckpointID *int   `json:"checkpoint_id"`
	Message      string `json:"message"`
}

// New creates one send_dmail tool.
func New(ctx MailContext) *Tool {
	return &Tool{Context: ctx}
}

// Name returns the tool name.
func (*Tool) Name() string {
	return toolName
}

// Description returns the tool description.
func (*Tool) Description() string {
	return toolDescription
}

// ParameterSchema returns the JSON schema for tool parameters.
func (*Tool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(parameterSchema)
}

// Execute reverts context and appends one mail message.
func (t *Tool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	if t == nil || t.Context == nil {
		return errorResult("send_dmail: context is not configured"), nil
	}

	if err := t.Context.RevertTo(*input.CheckpointID); err != nil {
		return errorResult(fmt.Sprintf("send_dmail: revert checkpoint %d: %v", *input.CheckpointID, err)), nil
	}

	if err := t.Context.Append(soul.Message{
		Role: soul.RoleUser,
		Content: types.ContentParts{
			types.TextPart{Text: input.Message},
		},
	}); err != nil {
		return errorResult(fmt.Sprintf("send_dmail: append message: %v", err)), nil
	}

	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"checkpoint_id": *input.CheckpointID,
				"message":       input.Message,
				"sent":          true,
			},
		},
	}, nil
}

func decodeParams(raw json.RawMessage) (executeParams, error) {
	input := executeParams{}
	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return executeParams{}, fmt.Errorf("send_dmail: decode params: %w", err)
	}

	if input.CheckpointID == nil {
		return executeParams{}, errors.New("send_dmail: checkpoint_id is required")
	}
	if *input.CheckpointID < 0 {
		return executeParams{}, errors.New("send_dmail: checkpoint_id must be >= 0")
	}

	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" {
		return executeParams{}, errors.New("send_dmail: message is required")
	}
	return input, nil
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

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
