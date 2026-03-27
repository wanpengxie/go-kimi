package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiewanpeng/go-kimi/pkg/kimi"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/config"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

func TestCrossPackageCoverageScenarioFromWire(t *testing.T) {
	t.Parallel()

	if kimi.Version == "" {
		t.Fatal("kimi.Version must not be empty")
	}

	cfg := config.NewDefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := config.SaveConfig(cfg, path); err != nil {
		t.Fatalf("config.SaveConfig() error = %v", err)
	}
	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}
	if loaded.DefaultProvider != cfg.DefaultProvider || loaded.DefaultModel != cfg.DefaultModel {
		t.Fatalf("loaded default provider/model mismatch: %#v vs %#v", loaded, cfg)
	}

	bad := cfg
	bad.DefaultProvider = "missing"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "default_provider") {
		t.Fatalf("Validate() error = %v, want default_provider related error", err)
	}

	if got := llm.ModelDisplayName("gpt-o1"); got != "GPT O1" {
		t.Fatalf("ModelDisplayName() = %q, want %q", got, "GPT O1")
	}
	derived := llm.DeriveModelCapabilities(config.LLMModel{
		Name:          "kimi-k2-vision-audio-video-json",
		ContextWindow: 128000,
	})
	if !derived[types.ModelCapabilityLongCtx] || !derived[types.ModelCapabilityVision] {
		t.Fatalf("unexpected derived capability set: %#v", derived)
	}

	parts := types.ContentParts{
		types.TextPart{Text: "hello"},
		types.ThinkPart{Think: "reason"},
		types.ImageURLPart{ImageURL: "https://example.com/image.png"},
		types.AudioURLPart{AudioURL: "https://example.com/audio.mp3"},
		types.VideoURLPart{VideoURL: "https://example.com/video.mp4"},
	}
	encodedParts, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("json.Marshal(ContentParts) error = %v", err)
	}
	var decodedParts types.ContentParts
	if err := json.Unmarshal(encodedParts, &decodedParts); err != nil {
		t.Fatalf("json.Unmarshal(ContentParts) error = %v", err)
	}
	if len(decodedParts) != len(parts) {
		t.Fatalf("decoded part count = %d, want %d", len(decodedParts), len(parts))
	}

	if _, err := types.UnmarshalContentPart([]byte(`{"type":"unknown","text":"x"}`)); err == nil {
		t.Fatal("UnmarshalContentPart() unknown type expected error")
	}
	if err := json.Unmarshal([]byte(`{"type":"bad","text":"x"}`), &types.TextPart{}); err == nil {
		t.Fatal("TextPart.UnmarshalJSON() unexpected type expected error")
	}
	if err := json.Unmarshal([]byte(`{"type":"bad","think":"x"}`), &types.ThinkPart{}); err == nil {
		t.Fatal("ThinkPart.UnmarshalJSON() unexpected type expected error")
	}
	if err := json.Unmarshal([]byte(`{"type":"bad","image_url":"x"}`), &types.ImageURLPart{}); err == nil {
		t.Fatal("ImageURLPart.UnmarshalJSON() unexpected type expected error")
	}
	if err := json.Unmarshal([]byte(`{"type":"bad","audio_url":"x"}`), &types.AudioURLPart{}); err == nil {
		t.Fatal("AudioURLPart.UnmarshalJSON() unexpected type expected error")
	}
	if err := json.Unmarshal([]byte(`{"type":"bad","video_url":"x"}`), &types.VideoURLPart{}); err == nil {
		t.Fatal("VideoURLPart.UnmarshalJSON() unexpected type expected error")
	}

	var zeroUsage types.TokenUsage
	usageJSON, err := json.Marshal(zeroUsage)
	if err != nil {
		t.Fatalf("json.Marshal(TokenUsage zero) error = %v", err)
	}
	if string(usageJSON) != "{}" {
		t.Fatalf("zero TokenUsage json = %s, want {}", usageJSON)
	}

	blocks := types.DisplayBlocks{
		types.BriefDisplayBlock{Text: "brief"},
		types.DiffDisplayBlock{Title: "diff", Diff: "@@ -1 +1 @@"},
		types.TodoDisplayBlock{
			Title: "todo",
			Items: []types.TodoDisplayItem{
				{Text: "a", Done: false},
				{Text: "b", Done: true},
			},
		},
		types.ShellDisplayBlock{Command: "go test", Output: "ok", ExitCode: 0},
		types.BackgroundTaskDisplayBlock{TaskID: "task-1", Status: "running"},
		types.UnknownDisplayBlock{
			OriginalType: "markdown",
			Payload: map[string]any{
				"text": "## title",
			},
		},
	}
	encodedBlocks, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("json.Marshal(DisplayBlocks) error = %v", err)
	}
	var decodedBlocks types.DisplayBlocks
	if err := json.Unmarshal(encodedBlocks, &decodedBlocks); err != nil {
		t.Fatalf("json.Unmarshal(DisplayBlocks) error = %v", err)
	}
	if len(decodedBlocks) != len(blocks) {
		t.Fatalf("decoded block count = %d, want %d", len(decodedBlocks), len(blocks))
	}
	if _, err := types.MarshalDisplayBlock(nil); err == nil {
		t.Fatal("MarshalDisplayBlock(nil) expected error")
	}
	if _, err := types.UnmarshalDisplayBlock([]byte(`{"text":"missing_type"}`)); err == nil {
		t.Fatal("UnmarshalDisplayBlock missing type expected error")
	}
	unknownBlock, err := types.UnmarshalDisplayBlock([]byte(`{"type":"custom","text":"x"}`))
	if err != nil {
		t.Fatalf("UnmarshalDisplayBlock unknown fallback error = %v", err)
	}
	if _, ok := unknownBlock.(types.UnknownDisplayBlock); !ok {
		t.Fatalf("unknown fallback type = %T, want %T", unknownBlock, types.UnknownDisplayBlock{})
	}

	badLoop := config.NewDefaultConfig()
	badLoop.Loop.MaxTurns = 0
	if err := badLoop.Validate(); err == nil || !strings.Contains(err.Error(), "loop.max_turns") {
		t.Fatalf("bad loop Validate() error = %v", err)
	}
	badBackground := config.NewDefaultConfig()
	badBackground.Background.TaskTimeoutSecond = 0
	if err := badBackground.Validate(); err == nil || !strings.Contains(err.Error(), "background.task_timeout_seconds") {
		t.Fatalf("bad background Validate() error = %v", err)
	}
	badProvider := config.NewDefaultConfig()
	badProvider.Providers[0].Name = " "
	if err := badProvider.Validate(); err == nil || !strings.Contains(err.Error(), "providers[0].name") {
		t.Fatalf("bad provider Validate() error = %v", err)
	}
	badModel := config.NewDefaultConfig()
	badModel.Models[0].Provider = " "
	if err := badModel.Validate(); err == nil || !strings.Contains(err.Error(), "models[0].provider") {
		t.Fatalf("bad model Validate() error = %v", err)
	}
	badDefaultModel := config.NewDefaultConfig()
	badDefaultModel.DefaultModel = "missing-model"
	if err := badDefaultModel.Validate(); err == nil || !strings.Contains(err.Error(), "default_model") {
		t.Fatalf("bad default model Validate() error = %v", err)
	}

	metadataOnlyFile := &WireFile{
		Metadata: WireFileMetadata{
			Path: filepath.Join(t.TempDir(), "wire", "events.jsonl"),
		},
	}
	if err := metadataOnlyFile.AppendMessage(TurnBegin{
		TurnID: "turn-1",
		Input:  parts[:2],
	}); err != nil {
		t.Fatalf("AppendMessage(metadata path) error = %v", err)
	}
	iter, err := metadataOnlyFile.IterRecords()
	if err != nil {
		t.Fatalf("IterRecords(metadata path) error = %v", err)
	}
	defer func() {
		if closeErr := iter.Close(); closeErr != nil {
			t.Fatalf("iterator close error = %v", closeErr)
		}
	}()
	if !iter.Next() {
		t.Fatal("expected at least one record")
	}
	if _, err := iter.Record().Message(); err != nil {
		t.Fatalf("record.Message() error = %v", err)
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iterator err = %v", err)
	}

	blockedPath := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blockedPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if err := config.SaveConfig(cfg, filepath.Join(blockedPath, "config.toml")); err == nil {
		t.Fatal("SaveConfig(path under regular file) expected error")
	}
}

func TestWireMarkerMethodsAreCallable(t *testing.T) {
	t.Parallel()

	events := []Event{
		TurnBegin{},
		TextDelta{},
		SteerInput{},
		TurnEnd{},
		StepBegin{},
		StepInterrupted{},
		CompactionBegin{},
		CompactionEnd{},
		MCPLoadingBegin{},
		MCPLoadingEnd{},
		StatusUpdate{},
		Notification{},
		SubagentEvent{},
		ToolCallResult{},
	}
	for i := range events {
		events[i].IsEvent()
	}

	requests := []Request{
		ApprovalRequest{},
		ApprovalResponse{},
		ToolCallRequest{},
		QuestionRequest{},
		QuestionResponse{},
		QuestionOption{},
		QuestionItem{},
	}
	for i := range requests {
		requests[i].IsRequest()
	}
}
