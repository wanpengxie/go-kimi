package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var (
	_ json.Marshaler   = TextPart{}
	_ json.Unmarshaler = (*TextPart)(nil)

	_ json.Marshaler   = ThinkPart{}
	_ json.Unmarshaler = (*ThinkPart)(nil)

	_ json.Marshaler   = ImageURLPart{}
	_ json.Unmarshaler = (*ImageURLPart)(nil)

	_ json.Marshaler   = AudioURLPart{}
	_ json.Unmarshaler = (*AudioURLPart)(nil)

	_ json.Marshaler   = VideoURLPart{}
	_ json.Unmarshaler = (*VideoURLPart)(nil)

	_ json.Marshaler   = ContentParts{}
	_ json.Unmarshaler = (*ContentParts)(nil)

	_ json.Marshaler   = ToolCall{}
	_ json.Unmarshaler = (*ToolCall)(nil)

	_ json.Marshaler   = ToolCallPart{}
	_ json.Unmarshaler = (*ToolCallPart)(nil)

	_ json.Marshaler   = ToolReturnValue{}
	_ json.Unmarshaler = (*ToolReturnValue)(nil)

	_ json.Marshaler   = ToolResult{}
	_ json.Unmarshaler = (*ToolResult)(nil)

	_ json.Marshaler   = UnknownDisplayBlock{}
	_ json.Unmarshaler = (*UnknownDisplayBlock)(nil)

	_ json.Marshaler   = BriefDisplayBlock{}
	_ json.Unmarshaler = (*BriefDisplayBlock)(nil)

	_ json.Marshaler   = DiffDisplayBlock{}
	_ json.Unmarshaler = (*DiffDisplayBlock)(nil)

	_ json.Marshaler   = TodoDisplayItem{}
	_ json.Unmarshaler = (*TodoDisplayItem)(nil)

	_ json.Marshaler   = TodoDisplayBlock{}
	_ json.Unmarshaler = (*TodoDisplayBlock)(nil)

	_ json.Marshaler   = ShellDisplayBlock{}
	_ json.Unmarshaler = (*ShellDisplayBlock)(nil)

	_ json.Marshaler   = BackgroundTaskDisplayBlock{}
	_ json.Unmarshaler = (*BackgroundTaskDisplayBlock)(nil)

	_ json.Marshaler   = DisplayBlocks{}
	_ json.Unmarshaler = (*DisplayBlocks)(nil)

	_ json.Marshaler   = TokenUsage{}
	_ json.Unmarshaler = (*TokenUsage)(nil)
)

func TestContentPartRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		part ContentPart
		want ContentPart
	}{
		{
			name: "text",
			part: TextPart{Text: "hello"},
			want: TextPart{Text: "hello"},
		},
		{
			name: "think",
			part: ThinkPart{Think: "trace"},
			want: ThinkPart{Think: "trace"},
		},
		{
			name: "image_url",
			part: ImageURLPart{ImageURL: "https://example.com/a.png"},
			want: ImageURLPart{ImageURL: "https://example.com/a.png"},
		},
		{
			name: "audio_url",
			part: AudioURLPart{AudioURL: "https://example.com/a.mp3"},
			want: AudioURLPart{AudioURL: "https://example.com/a.mp3"},
		},
		{
			name: "video_url",
			part: VideoURLPart{VideoURL: "https://example.com/a.mp4"},
			want: VideoURLPart{VideoURL: "https://example.com/a.mp4"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := MarshalContentPart(tc.part)
			if err != nil {
				t.Fatalf("MarshalContentPart() error = %v", err)
			}

			roundTrip, err := UnmarshalContentPart(data)
			if err != nil {
				t.Fatalf("UnmarshalContentPart() error = %v", err)
			}

			if !reflect.DeepEqual(roundTrip, tc.want) {
				t.Fatalf("roundTrip = %#v, want %#v", roundTrip, tc.want)
			}
		})
	}
}

func TestContentPartErrors(t *testing.T) {
	t.Parallel()

	if _, err := MarshalContentPart(nil); err == nil {
		t.Fatal("MarshalContentPart(nil) expected error")
	}

	_, err := UnmarshalContentPart([]byte(`{"text":"x"}`))
	if err == nil {
		t.Fatal("UnmarshalContentPart missing type expected error")
	}

	_, err = UnmarshalContentPart([]byte(`{"type":"unknown","text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("UnmarshalContentPart unknown type error = %v", err)
	}
}

func TestContentPartsJSONRoundTrip(t *testing.T) {
	t.Parallel()

	parts := ContentParts{
		TextPart{Text: "a"},
		ThinkPart{Think: "b"},
		ImageURLPart{ImageURL: "https://example.com/c.png"},
	}

	data, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("json.Marshal(ContentParts) error = %v", err)
	}

	var decoded ContentParts
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ContentParts) error = %v", err)
	}

	want := ContentParts{
		TextPart{Text: "a"},
		ThinkPart{Think: "b"},
		ImageURLPart{ImageURL: "https://example.com/c.png"},
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, want)
	}
}

func TestToolCallAndToolResultRoundTrip(t *testing.T) {
	t.Parallel()

	part := ToolCallPart{
		ToolCall: ToolCall{
			ID:   "call-1",
			Name: "search",
			Arguments: map[string]any{
				"query": "go test",
			},
		},
	}

	data, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("json.Marshal(ToolCallPart) error = %v", err)
	}

	var decodedPart ToolCallPart
	if err := json.Unmarshal(data, &decodedPart); err != nil {
		t.Fatalf("json.Unmarshal(ToolCallPart) error = %v", err)
	}

	if !reflect.DeepEqual(decodedPart, part) {
		t.Fatalf("decodedPart = %#v, want %#v", decodedPart, part)
	}

	result := ToolResult{
		ToolCallID: "call-1",
		Name:       "search",
		Value: ToolReturnValue{
			Value: map[string]any{
				"items": []any{"a", "b"},
			},
		},
	}
	resultData, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(ToolResult) error = %v", err)
	}

	var decodedResult ToolResult
	if err := json.Unmarshal(resultData, &decodedResult); err != nil {
		t.Fatalf("json.Unmarshal(ToolResult) error = %v", err)
	}

	if !reflect.DeepEqual(decodedResult, result) {
		t.Fatalf("decodedResult = %#v, want %#v", decodedResult, result)
	}
}

func TestToolCallPartTypeValidation(t *testing.T) {
	t.Parallel()

	var part ToolCallPart
	err := json.Unmarshal([]byte(`{"type":"bad","tool_call":{"id":"1","name":"x"}}`), &part)
	if err == nil {
		t.Fatal("json.Unmarshal(ToolCallPart) expected error")
	}
}

func TestDisplayBlockRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		block DisplayBlock
		want  DisplayBlock
	}{
		{
			name:  "brief",
			block: BriefDisplayBlock{Text: "summary"},
			want:  BriefDisplayBlock{Text: "summary"},
		},
		{
			name:  "diff",
			block: DiffDisplayBlock{Title: "patch", Diff: "@@ -1 +1 @@"},
			want:  DiffDisplayBlock{Title: "patch", Diff: "@@ -1 +1 @@"},
		},
		{
			name: "todo",
			block: TodoDisplayBlock{
				Title: "tasks",
				Items: []TodoDisplayItem{
					{Text: "a", Done: false},
					{Text: "b", Done: true},
				},
			},
			want: TodoDisplayBlock{
				Title: "tasks",
				Items: []TodoDisplayItem{
					{Text: "a", Done: false},
					{Text: "b", Done: true},
				},
			},
		},
		{
			name:  "shell",
			block: ShellDisplayBlock{Command: "go test ./...", Output: "ok", ExitCode: 0},
			want:  ShellDisplayBlock{Command: "go test ./...", Output: "ok", ExitCode: 0},
		},
		{
			name:  "background_task",
			block: BackgroundTaskDisplayBlock{TaskID: "bg-1", Status: "running", Message: "working"},
			want:  BackgroundTaskDisplayBlock{TaskID: "bg-1", Status: "running", Message: "working"},
		},
		{
			name: "unknown",
			block: UnknownDisplayBlock{
				OriginalType: "custom_markdown",
				Payload: map[string]any{
					"raw": "## heading",
				},
			},
			want: UnknownDisplayBlock{
				OriginalType: "custom_markdown",
				Payload: map[string]any{
					"raw": "## heading",
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := MarshalDisplayBlock(tc.block)
			if err != nil {
				t.Fatalf("MarshalDisplayBlock() error = %v", err)
			}

			roundTrip, err := UnmarshalDisplayBlock(data)
			if err != nil {
				t.Fatalf("UnmarshalDisplayBlock() error = %v", err)
			}

			if !reflect.DeepEqual(roundTrip, tc.want) {
				t.Fatalf("roundTrip = %#v, want %#v", roundTrip, tc.want)
			}
		})
	}
}

func TestDisplayBlockUnknownTypeFallback(t *testing.T) {
	t.Parallel()

	block, err := UnmarshalDisplayBlock([]byte(`{"type":"markdown","text":"hello","meta":{"x":"y"}}`))
	if err != nil {
		t.Fatalf("UnmarshalDisplayBlock() error = %v", err)
	}

	unknown, ok := block.(UnknownDisplayBlock)
	if !ok {
		t.Fatalf("block type = %T, want UnknownDisplayBlock", block)
	}
	if unknown.OriginalType != "markdown" {
		t.Fatalf("OriginalType = %q, want %q", unknown.OriginalType, "markdown")
	}

	if got := unknown.Payload["text"]; got != "hello" {
		t.Fatalf("Payload[text] = %#v, want %#v", got, "hello")
	}
}

func TestDisplayBlockErrors(t *testing.T) {
	t.Parallel()

	if _, err := MarshalDisplayBlock(nil); err == nil {
		t.Fatal("MarshalDisplayBlock(nil) expected error")
	}

	_, err := UnmarshalDisplayBlock([]byte(`{"text":"x"}`))
	if err == nil {
		t.Fatal("UnmarshalDisplayBlock missing type expected error")
	}
}

func TestDisplayBlocksJSONRoundTrip(t *testing.T) {
	t.Parallel()

	blocks := DisplayBlocks{
		BriefDisplayBlock{Text: "a"},
		TodoDisplayBlock{
			Items: []TodoDisplayItem{
				{Text: "b", Done: true},
			},
		},
	}

	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("json.Marshal(DisplayBlocks) error = %v", err)
	}

	var decoded DisplayBlocks
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(DisplayBlocks) error = %v", err)
	}

	want := DisplayBlocks{
		BriefDisplayBlock{Text: "a"},
		TodoDisplayBlock{
			Items: []TodoDisplayItem{
				{Text: "b", Done: true},
			},
		},
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, want)
	}
}

func TestTokenUsageJSONSnakeCase(t *testing.T) {
	t.Parallel()

	usage := TokenUsage{
		InputTokens:  12,
		OutputTokens: 34,
		TotalTokens:  46,
	}

	data, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("json.Marshal(TokenUsage) error = %v", err)
	}

	text := string(data)
	if !strings.Contains(text, "input_tokens") || !strings.Contains(text, "output_tokens") || !strings.Contains(text, "total_tokens") {
		t.Fatalf("TokenUsage json keys are not snake_case: %s", text)
	}

	var decoded TokenUsage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(TokenUsage) error = %v", err)
	}

	if decoded != usage {
		t.Fatalf("decoded = %#v, want %#v", decoded, usage)
	}
}
