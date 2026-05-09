// Package types defines shared cross-package data models.
package types

import (
	"encoding/json"
	"errors"
	"fmt"
)

// JsonType is a loose JSON value.
type JsonType = any

// ContentPartType identifies the concrete content part variant.
type ContentPartType string

const (
	ContentPartTypeText     ContentPartType = "text"
	ContentPartTypeThink    ContentPartType = "think"
	ContentPartTypeImageURL ContentPartType = "image_url"
	ContentPartTypeAudioURL ContentPartType = "audio_url"
	ContentPartTypeVideoURL ContentPartType = "video_url"
)

// ContentPart is a polymorphic content payload.
type ContentPart interface {
	isContentPart()
}

// TextPart carries plain text content.
type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) isContentPart() {}

func (p TextPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type ContentPartType `json:"type"`
		Text string          `json:"text"`
	}

	return json.Marshal(wire{
		Type: ContentPartTypeText,
		Text: p.Text,
	})
}

func (p *TextPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type ContentPartType `json:"type"`
		Text string          `json:"text"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ContentPartTypeText {
		return fmt.Errorf("text part: unexpected type %q", raw.Type)
	}
	p.Text = raw.Text
	return nil
}

// ThinkPart carries internal reasoning text.
//
// Signature carries the encrypted thinking proof Anthropic-style providers
// attach to thinking blocks (DeepSeek's anthropic-compat endpoint sets it
// too). It MUST be round-tripped verbatim into next-turn requests when
// present; otherwise the provider rejects the request with errors like
// "content[].thinking in the thinking mode must be passed back to the API"
// (DeepSeek) or 400 invalid_request_error (Anthropic).
type ThinkPart struct {
	Think     string `json:"think"`
	Signature string `json:"signature,omitempty"`
}

func (ThinkPart) isContentPart() {}

func (p ThinkPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type      ContentPartType `json:"type"`
		Think     string          `json:"think"`
		Signature string          `json:"signature,omitempty"`
	}

	return json.Marshal(wire{
		Type:      ContentPartTypeThink,
		Think:     p.Think,
		Signature: p.Signature,
	})
}

func (p *ThinkPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type      ContentPartType `json:"type"`
		Think     string          `json:"think"`
		Signature string          `json:"signature"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ContentPartTypeThink {
		return fmt.Errorf("think part: unexpected type %q", raw.Type)
	}
	p.Think = raw.Think
	p.Signature = raw.Signature
	return nil
}

// ImageURLPart references an image URL.
type ImageURLPart struct {
	ImageURL string `json:"image_url"`
}

func (ImageURLPart) isContentPart() {}

func (p ImageURLPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     ContentPartType `json:"type"`
		ImageURL string          `json:"image_url"`
	}

	return json.Marshal(wire{
		Type:     ContentPartTypeImageURL,
		ImageURL: p.ImageURL,
	})
}

func (p *ImageURLPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type     ContentPartType `json:"type"`
		ImageURL string          `json:"image_url"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ContentPartTypeImageURL {
		return fmt.Errorf("image url part: unexpected type %q", raw.Type)
	}
	p.ImageURL = raw.ImageURL
	return nil
}

// AudioURLPart references an audio URL.
type AudioURLPart struct {
	AudioURL string `json:"audio_url"`
}

func (AudioURLPart) isContentPart() {}

func (p AudioURLPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     ContentPartType `json:"type"`
		AudioURL string          `json:"audio_url"`
	}

	return json.Marshal(wire{
		Type:     ContentPartTypeAudioURL,
		AudioURL: p.AudioURL,
	})
}

func (p *AudioURLPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type     ContentPartType `json:"type"`
		AudioURL string          `json:"audio_url"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ContentPartTypeAudioURL {
		return fmt.Errorf("audio url part: unexpected type %q", raw.Type)
	}
	p.AudioURL = raw.AudioURL
	return nil
}

// VideoURLPart references a video URL.
type VideoURLPart struct {
	VideoURL string `json:"video_url"`
}

func (VideoURLPart) isContentPart() {}

func (p VideoURLPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     ContentPartType `json:"type"`
		VideoURL string          `json:"video_url"`
	}

	return json.Marshal(wire{
		Type:     ContentPartTypeVideoURL,
		VideoURL: p.VideoURL,
	})
}

func (p *VideoURLPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type     ContentPartType `json:"type"`
		VideoURL string          `json:"video_url"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ContentPartTypeVideoURL {
		return fmt.Errorf("video url part: unexpected type %q", raw.Type)
	}
	p.VideoURL = raw.VideoURL
	return nil
}

// ContentParts is a JSON-serializable list of ContentPart.
type ContentParts []ContentPart

func (p ContentParts) MarshalJSON() ([]byte, error) {
	raw := make([]json.RawMessage, len(p))
	for i := range p {
		marshaled, err := MarshalContentPart(p[i])
		if err != nil {
			return nil, fmt.Errorf("marshal content part[%d]: %w", i, err)
		}
		raw[i] = marshaled
	}
	return json.Marshal(raw)
}

func (p *ContentParts) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	parts := make(ContentParts, 0, len(raw))
	for i := range raw {
		part, err := UnmarshalContentPart(raw[i])
		if err != nil {
			return fmt.Errorf("unmarshal content part[%d]: %w", i, err)
		}
		parts = append(parts, part)
	}
	*p = parts
	return nil
}

// MarshalContentPart marshals a polymorphic content part.
func MarshalContentPart(part ContentPart) ([]byte, error) {
	if part == nil {
		return nil, errors.New("content part: nil")
	}

	switch typed := part.(type) {
	case TextPart:
		return json.Marshal(typed)
	case *TextPart:
		return json.Marshal(typed)
	case ThinkPart:
		return json.Marshal(typed)
	case *ThinkPart:
		return json.Marshal(typed)
	case ImageURLPart:
		return json.Marshal(typed)
	case *ImageURLPart:
		return json.Marshal(typed)
	case AudioURLPart:
		return json.Marshal(typed)
	case *AudioURLPart:
		return json.Marshal(typed)
	case VideoURLPart:
		return json.Marshal(typed)
	case *VideoURLPart:
		return json.Marshal(typed)
	default:
		return nil, fmt.Errorf("content part: unsupported type %T", part)
	}
}

// UnmarshalContentPart unmarshals a polymorphic content part.
func UnmarshalContentPart(data []byte) (ContentPart, error) {
	var discriminator struct {
		Type ContentPartType `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, err
	}

	switch discriminator.Type {
	case ContentPartTypeText:
		var part TextPart
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentPartTypeThink:
		var part ThinkPart
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentPartTypeImageURL:
		var part ImageURLPart
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentPartTypeAudioURL:
		var part AudioURLPart
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentPartTypeVideoURL:
		var part VideoURLPart
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case "":
		return nil, errors.New("content part: missing type")
	default:
		return nil, fmt.Errorf("content part: unknown type %q", discriminator.Type)
	}
}

// ToolCall captures a tool invocation request.
type ToolCall struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Arguments JsonType `json:"arguments,omitempty"`
}

func (c ToolCall) MarshalJSON() ([]byte, error) {
	type wire struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Arguments JsonType `json:"arguments,omitempty"`
	}

	return json.Marshal(wire(c))
}

func (c *ToolCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Arguments JsonType `json:"arguments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.ID = raw.ID
	c.Name = raw.Name
	c.Arguments = raw.Arguments
	return nil
}

// ToolCallPartType is the discriminator for ToolCallPart.
type ToolCallPartType string

const ToolCallPartTypeToolCall ToolCallPartType = "tool_call"

// ToolCallPart embeds a ToolCall as a typed payload.
type ToolCallPart struct {
	ToolCall ToolCall `json:"tool_call"`
}

func (p ToolCallPart) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     ToolCallPartType `json:"type"`
		ToolCall ToolCall         `json:"tool_call"`
	}

	return json.Marshal(wire{
		Type:     ToolCallPartTypeToolCall,
		ToolCall: p.ToolCall,
	})
}

func (p *ToolCallPart) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type     ToolCallPartType `json:"type"`
		ToolCall ToolCall         `json:"tool_call"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != ToolCallPartTypeToolCall {
		return fmt.Errorf("tool call part: unexpected type %q", raw.Type)
	}
	p.ToolCall = raw.ToolCall
	return nil
}

// ToolReturnValue is a JSON passthrough container.
type ToolReturnValue struct {
	Value JsonType
}

func (v ToolReturnValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Value)
}

func (v *ToolReturnValue) UnmarshalJSON(data []byte) error {
	var decoded JsonType
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	v.Value = decoded
	return nil
}

// ToolResult is a typed tool execution result.
type ToolResult struct {
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Value      ToolReturnValue `json:"value"`
	IsError    bool            `json:"is_error,omitempty"`
}

func (r ToolResult) MarshalJSON() ([]byte, error) {
	type wire struct {
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
		Value      ToolReturnValue `json:"value"`
		IsError    bool            `json:"is_error,omitempty"`
	}

	return json.Marshal(wire(r))
}

func (r *ToolResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
		Value      ToolReturnValue `json:"value"`
		IsError    bool            `json:"is_error"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.ToolCallID = raw.ToolCallID
	r.Name = raw.Name
	r.Value = raw.Value
	r.IsError = raw.IsError
	return nil
}

// DisplayBlockType identifies the concrete display block variant.
type DisplayBlockType string

const (
	DisplayBlockTypeUnknown        DisplayBlockType = "unknown"
	DisplayBlockTypeBrief          DisplayBlockType = "brief"
	DisplayBlockTypeDiff           DisplayBlockType = "diff"
	DisplayBlockTypeTodo           DisplayBlockType = "todo"
	DisplayBlockTypeShell          DisplayBlockType = "shell"
	DisplayBlockTypeBackgroundTask DisplayBlockType = "background_task"
)

// DisplayBlock is a polymorphic UI payload.
type DisplayBlock interface {
	isDisplayBlock()
}

// UnknownDisplayBlock stores an unsupported block type.
type UnknownDisplayBlock struct {
	OriginalType string              `json:"original_type,omitempty"`
	Payload      map[string]JsonType `json:"payload,omitempty"`
}

func (UnknownDisplayBlock) isDisplayBlock() {}

func (b UnknownDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type         DisplayBlockType    `json:"type"`
		OriginalType string              `json:"original_type,omitempty"`
		Payload      map[string]JsonType `json:"payload,omitempty"`
	}

	return json.Marshal(wire{
		Type:         DisplayBlockTypeUnknown,
		OriginalType: b.OriginalType,
		Payload:      b.Payload,
	})
}

func (b *UnknownDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type         DisplayBlockType    `json:"type"`
		OriginalType string              `json:"original_type"`
		Payload      map[string]JsonType `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeUnknown {
		return fmt.Errorf("unknown display block: unexpected type %q", raw.Type)
	}
	b.OriginalType = raw.OriginalType
	b.Payload = raw.Payload
	return nil
}

// BriefDisplayBlock is a short textual summary block.
type BriefDisplayBlock struct {
	Text string `json:"text"`
}

func (BriefDisplayBlock) isDisplayBlock() {}

func (b BriefDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type DisplayBlockType `json:"type"`
		Text string           `json:"text"`
	}

	return json.Marshal(wire{
		Type: DisplayBlockTypeBrief,
		Text: b.Text,
	})
}

func (b *BriefDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type DisplayBlockType `json:"type"`
		Text string           `json:"text"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeBrief {
		return fmt.Errorf("brief display block: unexpected type %q", raw.Type)
	}
	b.Text = raw.Text
	return nil
}

// DiffDisplayBlock carries text diff content.
type DiffDisplayBlock struct {
	Title string `json:"title,omitempty"`
	Diff  string `json:"diff"`
}

func (DiffDisplayBlock) isDisplayBlock() {}

func (b DiffDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type  DisplayBlockType `json:"type"`
		Title string           `json:"title,omitempty"`
		Diff  string           `json:"diff"`
	}

	return json.Marshal(wire{
		Type:  DisplayBlockTypeDiff,
		Title: b.Title,
		Diff:  b.Diff,
	})
}

func (b *DiffDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type  DisplayBlockType `json:"type"`
		Title string           `json:"title"`
		Diff  string           `json:"diff"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeDiff {
		return fmt.Errorf("diff display block: unexpected type %q", raw.Type)
	}
	b.Title = raw.Title
	b.Diff = raw.Diff
	return nil
}

// TodoDisplayItem is a single todo entry.
type TodoDisplayItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

func (i TodoDisplayItem) MarshalJSON() ([]byte, error) {
	type wire struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	}
	return json.Marshal(wire(i))
}

func (i *TodoDisplayItem) UnmarshalJSON(data []byte) error {
	var raw struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	i.Text = raw.Text
	i.Done = raw.Done
	return nil
}

// TodoDisplayBlock is a todo list block.
type TodoDisplayBlock struct {
	Title string            `json:"title,omitempty"`
	Items []TodoDisplayItem `json:"items"`
}

func (TodoDisplayBlock) isDisplayBlock() {}

func (b TodoDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type  DisplayBlockType  `json:"type"`
		Title string            `json:"title,omitempty"`
		Items []TodoDisplayItem `json:"items"`
	}

	return json.Marshal(wire{
		Type:  DisplayBlockTypeTodo,
		Title: b.Title,
		Items: b.Items,
	})
}

func (b *TodoDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type  DisplayBlockType  `json:"type"`
		Title string            `json:"title"`
		Items []TodoDisplayItem `json:"items"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeTodo {
		return fmt.Errorf("todo display block: unexpected type %q", raw.Type)
	}
	b.Title = raw.Title
	b.Items = raw.Items
	return nil
}

// ShellDisplayBlock carries shell execution feedback.
type ShellDisplayBlock struct {
	Command  string `json:"command"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func (ShellDisplayBlock) isDisplayBlock() {}

func (b ShellDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     DisplayBlockType `json:"type"`
		Command  string           `json:"command"`
		Output   string           `json:"output,omitempty"`
		ExitCode int              `json:"exit_code,omitempty"`
	}

	return json.Marshal(wire{
		Type:     DisplayBlockTypeShell,
		Command:  b.Command,
		Output:   b.Output,
		ExitCode: b.ExitCode,
	})
}

func (b *ShellDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type     DisplayBlockType `json:"type"`
		Command  string           `json:"command"`
		Output   string           `json:"output"`
		ExitCode int              `json:"exit_code"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeShell {
		return fmt.Errorf("shell display block: unexpected type %q", raw.Type)
	}
	b.Command = raw.Command
	b.Output = raw.Output
	b.ExitCode = raw.ExitCode
	return nil
}

// BackgroundTaskDisplayBlock carries background task status.
type BackgroundTaskDisplayBlock struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (BackgroundTaskDisplayBlock) isDisplayBlock() {}

func (b BackgroundTaskDisplayBlock) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type    DisplayBlockType `json:"type"`
		TaskID  string           `json:"task_id"`
		Status  string           `json:"status"`
		Message string           `json:"message,omitempty"`
	}

	return json.Marshal(wire{
		Type:    DisplayBlockTypeBackgroundTask,
		TaskID:  b.TaskID,
		Status:  b.Status,
		Message: b.Message,
	})
}

func (b *BackgroundTaskDisplayBlock) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    DisplayBlockType `json:"type"`
		TaskID  string           `json:"task_id"`
		Status  string           `json:"status"`
		Message string           `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type != "" && raw.Type != DisplayBlockTypeBackgroundTask {
		return fmt.Errorf("background task display block: unexpected type %q", raw.Type)
	}
	b.TaskID = raw.TaskID
	b.Status = raw.Status
	b.Message = raw.Message
	return nil
}

// DisplayBlocks is a JSON-serializable list of DisplayBlock.
type DisplayBlocks []DisplayBlock

func (b DisplayBlocks) MarshalJSON() ([]byte, error) {
	raw := make([]json.RawMessage, len(b))
	for i := range b {
		marshaled, err := MarshalDisplayBlock(b[i])
		if err != nil {
			return nil, fmt.Errorf("marshal display block[%d]: %w", i, err)
		}
		raw[i] = marshaled
	}
	return json.Marshal(raw)
}

func (b *DisplayBlocks) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	blocks := make(DisplayBlocks, 0, len(raw))
	for i := range raw {
		block, err := UnmarshalDisplayBlock(raw[i])
		if err != nil {
			return fmt.Errorf("unmarshal display block[%d]: %w", i, err)
		}
		blocks = append(blocks, block)
	}
	*b = blocks
	return nil
}

// MarshalDisplayBlock marshals a polymorphic display block.
func MarshalDisplayBlock(block DisplayBlock) ([]byte, error) {
	if block == nil {
		return nil, errors.New("display block: nil")
	}

	switch typed := block.(type) {
	case UnknownDisplayBlock:
		return json.Marshal(typed)
	case *UnknownDisplayBlock:
		return json.Marshal(typed)
	case BriefDisplayBlock:
		return json.Marshal(typed)
	case *BriefDisplayBlock:
		return json.Marshal(typed)
	case DiffDisplayBlock:
		return json.Marshal(typed)
	case *DiffDisplayBlock:
		return json.Marshal(typed)
	case TodoDisplayBlock:
		return json.Marshal(typed)
	case *TodoDisplayBlock:
		return json.Marshal(typed)
	case ShellDisplayBlock:
		return json.Marshal(typed)
	case *ShellDisplayBlock:
		return json.Marshal(typed)
	case BackgroundTaskDisplayBlock:
		return json.Marshal(typed)
	case *BackgroundTaskDisplayBlock:
		return json.Marshal(typed)
	default:
		return nil, fmt.Errorf("display block: unsupported type %T", block)
	}
}

// UnmarshalDisplayBlock unmarshals a polymorphic display block.
func UnmarshalDisplayBlock(data []byte) (DisplayBlock, error) {
	var discriminator struct {
		Type DisplayBlockType `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, err
	}

	switch discriminator.Type {
	case DisplayBlockTypeUnknown:
		var block UnknownDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case DisplayBlockTypeBrief:
		var block BriefDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case DisplayBlockTypeDiff:
		var block DiffDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case DisplayBlockTypeTodo:
		var block TodoDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case DisplayBlockTypeShell:
		var block ShellDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case DisplayBlockTypeBackgroundTask:
		var block BackgroundTaskDisplayBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "":
		return nil, errors.New("display block: missing type")
	default:
		payload := map[string]JsonType{}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		delete(payload, "type")
		return UnknownDisplayBlock{
			OriginalType: string(discriminator.Type),
			Payload:      payload,
		}, nil
	}
}

// TokenUsage captures token usage counters. Cache-related counters surface
// the Anthropic-style prompt-cache hit/miss split (also returned verbatim by
// DeepSeek's anthropic-compat endpoint), so multi-turn callers can measure
// effective cost / cache-hit ratio without re-parsing wire payloads.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	TotalTokens              int `json:"total_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

func (u TokenUsage) MarshalJSON() ([]byte, error) {
	type wire struct {
		InputTokens              int `json:"input_tokens,omitempty"`
		OutputTokens             int `json:"output_tokens,omitempty"`
		TotalTokens              int `json:"total_tokens,omitempty"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	}
	return json.Marshal(wire(u))
}

func (u *TokenUsage) UnmarshalJSON(data []byte) error {
	var raw struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		TotalTokens              int `json:"total_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.InputTokens = raw.InputTokens
	u.OutputTokens = raw.OutputTokens
	u.TotalTokens = raw.TotalTokens
	u.CacheCreationInputTokens = raw.CacheCreationInputTokens
	u.CacheReadInputTokens = raw.CacheReadInputTokens
	return nil
}
