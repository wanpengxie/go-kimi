//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanpengxie/go-kimi/internal/soul"
	kimi "github.com/wanpengxie/go-kimi/pkg/kimi"
	"github.com/wanpengxie/go-kimi/pkg/kimi/agentspec"
	approvalruntime "github.com/wanpengxie/go-kimi/pkg/kimi/approval"
	corebg "github.com/wanpengxie/go-kimi/pkg/kimi/background"
	"github.com/wanpengxie/go-kimi/pkg/kimi/config"
	kimierrors "github.com/wanpengxie/go-kimi/pkg/kimi/errors"
	"github.com/wanpengxie/go-kimi/pkg/kimi/llm"
	"github.com/wanpengxie/go-kimi/pkg/kimi/mcp"
	"github.com/wanpengxie/go-kimi/pkg/kimi/session"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	dmailtool "github.com/wanpengxie/go-kimi/pkg/kimi/tools/dmail"
	toolfile "github.com/wanpengxie/go-kimi/pkg/kimi/tools/file"
	"github.com/wanpengxie/go-kimi/pkg/kimi/tools/question"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
	"github.com/wanpengxie/go-kimi/pkg/kimi/wire"
)

func TestAgentFacadeBasicTurn(t *testing.T) {
	t.Parallel()

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: "agent facade ok"}},
			{Done: true},
		}},
	}

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  t.TempDir(),
		Config:   m8ScriptedConfig(),
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(agent.LastResult().Content)); got != "agent facade ok" {
		t.Fatalf("LastResult text = %q, want %q", got, "agent facade ok")
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider.CallCount() = %d, want 1", provider.CallCount())
	}
}

func TestAgentFacadeToolExecution(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "m8-call-echo", Name: "m8_echo", Arguments: map[string]any{"message": "hello-tool"}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "tool flow completed"}},
				{Done: true},
			},
		},
	}

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:         t.TempDir(),
		Config:          m8ScriptedConfig(),
		Provider:        provider,
		AdditionalTools: []tools.Tool{&m8EchoTool{calls: &calls}},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	if err := agent.Run(context.Background(), "call m8_echo"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("m8_echo execute count = %d, want 1", got)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider.CallCount() = %d, want 2", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if !m8MessagesContainText(requests[1].Messages, "m8-echo:hello-tool") {
		t.Fatalf("second request missing tool output m8-echo:hello-tool, messages=%#v", requests[1].Messages)
	}
}

func TestAgentFacadeApproval(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()
	events := runtime.Subscribe()
	defer runtime.Unsubscribe(events)

	var calls atomic.Int32
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "m8-call-approval", Name: "m8_echo", Arguments: map[string]any{"message": "approval"}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "approval done"}},
				{Done: true},
			},
		},
	}

	forceYoloOff := false
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:         t.TempDir(),
		Config:          m8ScriptedConfig(),
		Provider:        provider,
		AdditionalTools: []tools.Tool{&m8EchoTool{calls: &calls}},
		ApprovalRuntime: runtime,
		Overrides: kimi.AgentOverrides{
			DefaultYolo: &forceYoloOff,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	outcome := make(chan error, 1)
	go func() {
		outcome <- agent.Run(context.Background(), "run approval tool")
	}()

	created := m8WaitApprovalEvent(t, events, approvalruntime.EventRequestCreated, 2*time.Second)
	if created.Record == nil {
		t.Fatal("created approval event missing record")
	}
	if err := runtime.Resolve(created.Record.ID, approvalruntime.ApprovalApprove, ""); err != nil {
		t.Fatalf("runtime.Resolve() error = %v", err)
	}

	resolved := m8WaitApprovalEvent(t, events, approvalruntime.EventRequestResolved, 2*time.Second)
	if resolved.Record == nil || resolved.Record.Decision == nil || *resolved.Record.Decision != approvalruntime.ApprovalApprove {
		t.Fatalf("resolved event = %#v, want approve", resolved)
	}

	if err := <-outcome; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("tool execute count = %d, want 1", got)
	}
}

func TestAgentFacadeWireEvents(t *testing.T) {
	t.Parallel()

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: "wire events"}},
			{Done: true},
		}},
	}
	wireCh := make(chan wire.WireMessage, 16)

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:     t.TempDir(),
		Config:      m8ScriptedConfig(),
		Provider:    provider,
		WireEmitter: wire.ChannelEmitter{Ch: wireCh},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	if err := agent.Run(context.Background(), "emit wire events"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	events := drainWireMessages(wireCh)
	if len(events) < 3 {
		t.Fatalf("wire event count = %d, want >= 3", len(events))
	}
	if _, ok := events[0].(wire.TurnBegin); !ok {
		t.Fatalf("event[0] = %T, want wire.TurnBegin", events[0])
	}
	if !m8HasWireType[wire.TextDelta](events) {
		t.Fatalf("wire events missing TextDelta: %#v", events)
	}
	if !m8HasWireType[wire.TurnEnd](events) {
		t.Fatalf("wire events missing TurnEnd: %#v", events)
	}
}

func TestAgentSteer(t *testing.T) {
	t.Parallel()

	blocking := &m8BlockingTool{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{ToolCall: &types.ToolCall{ID: "m8-call-block", Name: "m8_block", Arguments: map[string]any{}}},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "steer consumed"}},
				{Done: true},
			},
		},
	}
	wireCh := make(chan wire.WireMessage, 32)

	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:         t.TempDir(),
		Config:          m8ScriptedConfig(),
		Provider:        provider,
		AdditionalTools: []tools.Tool{blocking},
		WireEmitter:     wire.ChannelEmitter{Ch: wireCh},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	outcome := make(chan error, 1)
	go func() {
		outcome <- agent.Run(context.Background(), "run blocking tool then continue")
	}()

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting m8_block tool entry")
	}

	if err := agent.Steer(context.Background(), "steer-token-m8"); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	close(blocking.release)

	if err := <-outcome; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(requests))
	}
	if !m8MessagesContainText(requests[1].Messages, "steer-token-m8") {
		t.Fatalf("second request missing steer message, messages=%#v", requests[1].Messages)
	}
	if !m8HasSteerEvent(wireCh, "steer-token-m8") {
		t.Fatal("wire events missing SteerInput for steer-token-m8")
	}
}

func TestCheckpointRevert(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	if err := ctxStore.Append(soul.Message{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: "turn1 user"}}}); err != nil {
		t.Fatalf("Append(user) error = %v", err)
	}
	if err := ctxStore.Append(soul.Message{Role: soul.RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "turn1 assistant"}}}); err != nil {
		t.Fatalf("Append(assistant) error = %v", err)
	}
	checkpoint := ctxStore.Checkpoint()
	if checkpoint != 2 {
		t.Fatalf("Checkpoint() = %d, want 2", checkpoint)
	}

	if err := ctxStore.Append(soul.Message{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: "turn2 user"}}}); err != nil {
		t.Fatalf("Append(turn2 user) error = %v", err)
	}
	if err := ctxStore.RevertTo(checkpoint); err != nil {
		t.Fatalf("RevertTo(%d) error = %v", checkpoint, err)
	}
	messages := ctxStore.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count after revert = %d, want 2", len(messages))
	}
	if got := strings.TrimSpace(textFromContentParts(messages[1].Content)); got != "turn1 assistant" {
		t.Fatalf("messages[1] = %q, want %q", got, "turn1 assistant")
	}
}

func TestSendDMailTool(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	seed := []soul.Message{
		{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: "u1"}}},
		{Role: soul.RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a1"}}},
		{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: "u2"}}},
		{Role: soul.RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "a2"}}},
	}
	for i := range seed {
		if err := ctxStore.Append(seed[i]); err != nil {
			t.Fatalf("Append(seed[%d]) error = %v", i, err)
		}
	}

	tool := dmailtool.New(ctxStore)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"checkpoint_id":2,"message":"mail follow up"}`))
	if err != nil {
		t.Fatalf("send_dmail Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("send_dmail result.IsError = true, value=%#v", result.Value.Value)
	}

	messages := ctxStore.Messages()
	if len(messages) != 3 {
		t.Fatalf("message count after send_dmail = %d, want 3", len(messages))
	}
	if messages[2].Role != soul.RoleUser {
		t.Fatalf("messages[2].Role = %q, want user", messages[2].Role)
	}
	if got := strings.TrimSpace(textFromContentParts(messages[2].Content)); got != "mail follow up" {
		t.Fatalf("messages[2] text = %q, want %q", got, "mail follow up")
	}
}

func TestAskUserQuestionTool(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(8)
	defer hub.Close()
	subscriber := hub.Subscribe()
	defer hub.Unsubscribe(subscriber)

	tool := question.New(hub, hub, func() bool { return false })

	resultCh := make(chan types.ToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := tool.Execute(context.Background(), json.RawMessage(`{
		  "prompt":"please answer",
		  "questions":[{"id":"q1","question":"continue?","options":[{"label":"yes"},{"label":"no"}]}],
		  "timeout_seconds": 5
		}`))
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var request wire.QuestionRequest
	select {
	case msg := <-subscriber:
		typed, ok := msg.(wire.QuestionRequest)
		if !ok {
			t.Fatalf("first hub message type = %T, want wire.QuestionRequest", msg)
		}
		request = typed
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting question request")
	}

	if err := hub.Emit(wire.QuestionResponse{
		RequestID: request.ID,
		Answers:   map[string]string{"q1": "yes"},
	}); err != nil {
		t.Fatalf("hub.Emit(question response) error = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ask_user_question Execute() error = %v", err)
	case result := <-resultCh:
		if result.IsError {
			t.Fatalf("ask_user_question result.IsError = true, value=%#v", result.Value.Value)
		}
		payload, ok := result.Value.Value.(map[string]any)
		if !ok {
			t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
		}
		answers, ok := payload["answers"].(map[string]string)
		if !ok {
			t.Fatalf("answers type = %T, want map[string]string", payload["answers"])
		}
		if answers["q1"] != "yes" {
			t.Fatalf("answers[q1] = %q, want yes", answers["q1"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting ask_user_question result")
	}
}

func TestReadMediaFileTool(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mediaPath := filepath.Join(workDir, "pixel.png")
	const oneByOnePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z8XcAAAAASUVORK5CYII="
	decoded, err := base64.StdEncoding.DecodeString(oneByOnePNG)
	if err != nil {
		t.Fatalf("DecodeString(oneByOnePNG) error = %v", err)
	}
	if err := os.WriteFile(mediaPath, decoded, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", mediaPath, err)
	}

	tool := toolfile.NewReadMediaFile(workDir, true, false)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"pixel.png"}`))
	if err != nil {
		t.Fatalf("read_media_file Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("read_media_file result.IsError = true, value=%#v", result.Value.Value)
	}

	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	parts, ok := payload["content_parts"].(types.ContentParts)
	if !ok || len(parts) == 0 {
		t.Fatalf("content_parts = %#v, want non-empty types.ContentParts", payload["content_parts"])
	}
	imagePart, ok := parts[0].(types.ImageURLPart)
	if !ok {
		t.Fatalf("content_parts[0] type = %T, want types.ImageURLPart", parts[0])
	}
	if !strings.HasPrefix(imagePart.ImageURL, "data:image/") {
		t.Fatalf("ImageURL = %q, want prefix data:image/", imagePart.ImageURL)
	}
}

func TestAgentSpecYAML(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, "base.yaml")
	childPath := filepath.Join(root, "child.yaml")

	if err := os.WriteFile(basePath, []byte(`name: base
system_prompt: "You are SpecBase"
tools:
  allowed_tools: ["think"]
`), 0o644); err != nil {
		t.Fatalf("write base spec error = %v", err)
	}
	if err := os.WriteFile(childPath, []byte(`name: child
extends: "base.yaml"
model: "scripted-model"
`), 0o644); err != nil {
		t.Fatalf("write child spec error = %v", err)
	}

	resolved, err := agentspec.ResolveAgentSpec(childPath)
	if err != nil {
		t.Fatalf("ResolveAgentSpec() error = %v", err)
	}
	if resolved.Name != "child" {
		t.Fatalf("resolved.Name = %q, want child", resolved.Name)
	}
	if len(resolved.AllowedTools) != 1 || resolved.AllowedTools[0] != "think" {
		t.Fatalf("resolved.AllowedTools = %#v, want [think]", resolved.AllowedTools)
	}

	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{{
			{Delta: types.TextPart{Text: "spec run"}},
			{Done: true},
		}},
	}
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  root,
		Config:   m8ScriptedConfig(),
		Provider: provider,
		SpecPath: childPath,
	})
	if err != nil {
		t.Fatalf("NewAgent(spec_path) error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	if err := agent.Run(context.Background(), "hello spec"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "think" {
		t.Fatalf("request tools = %#v, want only think", requests[0].Tools)
	}
	if !m8MessagesContainText(requests[0].Messages, "You are SpecBase") {
		t.Fatalf("request messages missing system prompt from spec, messages=%#v", requests[0].Messages)
	}
}

func TestWireHubBroadcast(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(8)
	defer hub.Close()

	s1 := hub.Subscribe()
	s2 := hub.Subscribe()
	s3 := hub.Subscribe()
	defer hub.Unsubscribe(s1)
	defer hub.Unsubscribe(s2)
	defer hub.Unsubscribe(s3)

	payload := wire.Notification{Message: "broadcast-token"}
	hub.Publish(payload)

	for i, ch := range []<-chan wire.WireMessage{s1, s2, s3} {
		select {
		case msg := <-ch:
			n, ok := msg.(wire.Notification)
			if !ok || n.Message != payload.Message {
				t.Fatalf("subscriber[%d] message = %#v, want notification %q", i, msg, payload.Message)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber[%d] did not receive broadcast", i)
		}
	}
}

func TestWireRecorder(t *testing.T) {
	t.Parallel()

	wirePath := filepath.Join(t.TempDir(), "wire.jsonl")
	source := make(chan wire.WireMessage, 4)
	recorder := wire.NewRecorder(wire.NewWireFile(wirePath), source)

	source <- wire.TurnBegin{TurnID: "turn-recorder"}
	source <- wire.TextDelta{TurnID: "turn-recorder", Delta: "hello"}
	source <- wire.TurnEnd{TurnID: "turn-recorder", StopReason: "stop"}
	close(source)

	if err := recorder.Close(); err != nil {
		t.Fatalf("Recorder.Close() error = %v", err)
	}

	iter, err := wire.NewWireFile(wirePath).IterRecords()
	if err != nil {
		t.Fatalf("IterRecords() error = %v", err)
	}
	defer func() {
		if closeErr := iter.Close(); closeErr != nil {
			t.Fatalf("iterator Close() error = %v", closeErr)
		}
	}()

	count := 0
	for iter.Next() {
		count++
	}
	if iter.Err() != nil {
		t.Fatalf("iterator err = %v", iter.Err())
	}
	if count != 3 {
		t.Fatalf("record count = %d, want 3", count)
	}
}

func TestWireMergingSubscriber(t *testing.T) {
	t.Parallel()

	hub := wire.NewHub(8)
	merger := wire.NewMergingSubscriber(hub, 8)
	defer merger.Close()
	defer hub.Close()

	hub.Publish(wire.TurnBegin{TurnID: "turn-merge"})
	hub.Publish(wire.TextDelta{TurnID: "turn-merge", Delta: "hello"})
	hub.Publish(wire.TextDelta{TurnID: "turn-merge", Delta: " world"})
	hub.Publish(wire.TurnEnd{TurnID: "turn-merge", StopReason: "stop"})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-merger.Messages():
			if msg == nil {
				continue
			}
			turnEnd, ok := msg.(wire.TurnEnd)
			if !ok {
				continue
			}
			if got := strings.TrimSpace(textFromContentParts(turnEnd.Output)); got != "hello world" {
				t.Fatalf("merged turn output = %q, want %q", got, "hello world")
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting merged turn_end event")
		}
	}
}

func TestSessionPlanModeState(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	s.State.PlanMode = true
	s.State.PlanSessionID = s.ID
	s.State.PlanSlug = "scripted-m8-plan"
	if err := s.SaveState(); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	found, err := session.Find(workDir, s.ID)
	if err != nil {
		t.Fatalf("session.Find() error = %v", err)
	}
	if !found.State.PlanMode {
		t.Fatal("found.State.PlanMode = false, want true")
	}
	if found.State.PlanSessionID != s.ID {
		t.Fatalf("PlanSessionID = %q, want %q", found.State.PlanSessionID, s.ID)
	}
	if found.State.PlanSlug != "scripted-m8-plan" {
		t.Fatalf("PlanSlug = %q, want scripted-m8-plan", found.State.PlanSlug)
	}
}

func TestErrorSentinels(t *testing.T) {
	t.Parallel()

	cfg := m8ScriptedConfig()
	cfg.Loop.MaxTurns = 1
	agent, err := kimi.NewAgent(kimi.AgentConfig{
		WorkDir:  t.TempDir(),
		Config:   cfg,
		Provider: &m8LoopingProvider{},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	defer func() {
		if closeErr := agent.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	err = agent.Run(context.Background(), "loop")
	if !errors.Is(err, kimierrors.ErrMaxStepsReached) {
		t.Fatalf("Run() error = %v, want ErrMaxStepsReached", err)
	}
}

func TestErrorTyped(t *testing.T) {
	t.Parallel()

	t.Run("ConfigError", func(t *testing.T) {
		_, err := kimi.NewAgent(kimi.AgentConfig{
			WorkDir:    t.TempDir(),
			ConfigPath: filepath.Join(t.TempDir(), "missing.toml"),
		})
		var typed *kimierrors.ConfigError
		if !errors.As(err, &typed) {
			t.Fatalf("error = %v, want *ConfigError", err)
		}
	})

	t.Run("ToolError", func(t *testing.T) {
		_, err := kimi.NewAgent(kimi.AgentConfig{
			WorkDir: t.TempDir(),
			Config:  m8ScriptedConfig(),
			Spec: &agentspec.ResolvedSpec{
				Name:         "bad-tools",
				AllowedTools: []string{"not_exists_tool"},
			},
		})
		var typed *kimierrors.ToolError
		if !errors.As(err, &typed) {
			t.Fatalf("error = %v, want *ToolError", err)
		}
	})

	t.Run("LLMError", func(t *testing.T) {
		agent, err := kimi.NewAgent(kimi.AgentConfig{
			WorkDir:  t.TempDir(),
			Config:   m8ScriptedConfig(),
			Provider: &m8StepErrorProvider{},
		})
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}
		defer func() {
			_ = agent.Close()
		}()

		err = agent.Run(context.Background(), "trigger llm error")
		var typed *kimierrors.LLMError
		if !errors.As(err, &typed) {
			t.Fatalf("error = %v, want *LLMError", err)
		}
	})
}

func TestMCPPagination(t *testing.T) {
	t.Parallel()

	transport := &m8PaginationTransport{}
	client := mcp.NewMCPClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	defs, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("len(defs) = %d, want 2", len(defs))
	}
	if defs[0].Name != "first" || defs[1].Name != "second" {
		t.Fatalf("tool names = [%s, %s], want [first, second]", defs[0].Name, defs[1].Name)
	}
	if got := transport.listCalls.Load(); got != 2 {
		t.Fatalf("tools/list call count = %d, want 2", got)
	}
}

func TestBackgroundTaskWait(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}
	manager := newE2EBackgroundManager(t, s.TasksDir(), nil)

	taskID, err := manager.CreateBashTask(context.Background(), corebg.TaskSpec{
		SessionID:   s.ID,
		Description: "m8 wait",
		Command:     "printf wait-ok",
		TimeoutSec:  5,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Wait(waitCtx, taskID); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	view, err := manager.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if view.Runtime.Status != corebg.TaskCompleted {
		t.Fatalf("task status = %q, want completed", view.Runtime.Status)
	}
}

func TestBackgroundTailOutput(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	s, err := session.Create(workDir)
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}
	manager := newE2EBackgroundManager(t, s.TasksDir(), nil)

	taskID, err := manager.CreateBashTask(context.Background(), corebg.TaskSpec{
		SessionID:   s.ID,
		Description: "m8 tail",
		Command:     "printf tail-output-token",
		TimeoutSec:  5,
	})
	if err != nil {
		t.Fatalf("CreateBashTask() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Wait(waitCtx, taskID); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	chunk, err := manager.TailOutput(taskID, 0, 0)
	if err != nil {
		t.Fatalf("TailOutput() error = %v", err)
	}
	if !strings.Contains(chunk.Output, "tail-output-token") {
		t.Fatalf("TailOutput().Output = %q, want contains tail-output-token", chunk.Output)
	}
	if !chunk.EOF {
		t.Fatalf("TailOutput().EOF = false, want true")
	}
}

func m8ScriptedConfig() config.Config {
	cfg := config.NewDefaultConfig()
	cfg.Services.MoonshotFetch.Enabled = false
	cfg.Services.MoonshotSearch.Enabled = false
	cfg.MCP.Clients = nil
	return cfg
}

type m8EchoTool struct {
	calls *atomic.Int32
}

func (*m8EchoTool) Name() string        { return "m8_echo" }
func (*m8EchoTool) Description() string { return "echo helper for scripted m8 tests" }
func (*m8EchoTool) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)
}
func (t *m8EchoTool) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	if t != nil && t.calls != nil {
		t.calls.Add(1)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return types.ToolResult{}, err
	}
	return types.ToolResult{
		Name:  "m8_echo",
		Value: types.ToolReturnValue{Value: "m8-echo:" + strings.TrimSpace(payload.Message)},
	}, nil
}

type m8BlockingTool struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*m8BlockingTool) Name() string                     { return "m8_block" }
func (*m8BlockingTool) Description() string              { return "blocks until released" }
func (*m8BlockingTool) ParameterSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *m8BlockingTool) Execute(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.once.Do(func() { t.entered <- struct{}{} })
	select {
	case <-t.release:
	case <-ctx.Done():
		return types.ToolResult{}, ctx.Err()
	}
	return types.ToolResult{Name: "m8_block", Value: types.ToolReturnValue{Value: "released"}}, nil
}

type m8LoopingProvider struct{}

func (*m8LoopingProvider) ModelName() string                        { return "m8-loop" }
func (p *m8LoopingProvider) WithModel(_ string) llm.ChatProvider    { return p }
func (p *m8LoopingProvider) WithThinking(_ string) llm.ChatProvider { return p }
func (*m8LoopingProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (*m8LoopingProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	ch := make(chan llm.ChatEvent, 2)
	ch <- llm.ChatEvent{ToolCall: &types.ToolCall{ID: "loop", Name: "m8_echo", Arguments: map[string]any{"message": "loop"}}}
	ch <- llm.ChatEvent{Done: true}
	close(ch)
	return ch, nil
}

type m8StepErrorProvider struct{}

func (*m8StepErrorProvider) ModelName() string                        { return "m8-step-error" }
func (p *m8StepErrorProvider) WithModel(_ string) llm.ChatProvider    { return p }
func (p *m8StepErrorProvider) WithThinking(_ string) llm.ChatProvider { return p }
func (*m8StepErrorProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (*m8StepErrorProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	ch := make(chan llm.ChatEvent, 1)
	ch <- llm.ChatEvent{Err: errors.New("scripted llm failure")}
	close(ch)
	return ch, nil
}

type m8PaginationTransport struct {
	listCalls atomic.Int32
}

func (t *m8PaginationTransport) Send(_ context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "initialize":
		return json.RawMessage(`{"protocolVersion":"2026-03-26","capabilities":{},"serverInfo":{"name":"scripted-m8"}}`), nil
	case "notifications/initialized":
		return json.RawMessage(`{}`), nil
	case "tools/list":
		t.listCalls.Add(1)
		cursor := ""
		if m, ok := params.(map[string]any); ok {
			if rawCursor, exists := m["cursor"]; exists && rawCursor != nil {
				cursor = strings.TrimSpace(fmt.Sprintf("%v", rawCursor))
			}
		}
		if cursor == "" {
			return json.RawMessage(`{"tools":[{"name":"first","description":"first tool","inputSchema":{"type":"object"}}],"nextCursor":"page-2"}`), nil
		}
		if cursor == "page-2" {
			return json.RawMessage(`{"tools":[{"name":"second","description":"second tool","inputSchema":{"type":"object"}}]}`), nil
		}
		return nil, fmt.Errorf("unexpected cursor: %q", cursor)
	default:
		return nil, fmt.Errorf("unexpected method: %s", method)
	}
}

func (*m8PaginationTransport) Close() error { return nil }

func m8MessagesContainText(messages []llm.Message, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for i := range messages {
		if strings.Contains(textFromContentParts(messages[i].Content), needle) {
			return true
		}
	}
	return false
}

func m8WaitApprovalEvent(
	t *testing.T,
	events <-chan approvalruntime.Event,
	kind approvalruntime.EventKind,
	timeout time.Duration,
) approvalruntime.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if event.Kind == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("timeout waiting approval event kind=%s", kind)
			return approvalruntime.Event{}
		}
	}
}

func m8HasSteerEvent(ch <-chan wire.WireMessage, text string) bool {
	events := drainWireMessages(ch)
	for i := range events {
		steer, ok := events[i].(wire.SteerInput)
		if !ok {
			continue
		}
		if strings.TrimSpace(steer.Text) == strings.TrimSpace(text) {
			return true
		}
	}
	return false
}

func m8HasWireType[T any](events []wire.WireMessage) bool {
	for i := range events {
		if _, ok := events[i].(T); ok {
			return true
		}
	}
	return false
}
