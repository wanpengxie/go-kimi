//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiewanpeng/go-kimi/internal/soul"
	approvalruntime "github.com/xiewanpeng/go-kimi/pkg/kimi/approval"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/plan"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/web"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

func TestScriptedApprovalRuntimeLifecycle(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()
	sub := runtime.Subscribe()
	defer runtime.Unsubscribe(sub)

	record, decisionCh, err := runtime.CreateRequest(
		context.Background(),
		approvalruntime.ApprovalSource{Kind: approvalruntime.SourceForegroundTurn, ID: "turn-scripted-m5"},
		"shell",
		"tool=shell args={\"command\":\"printf hi\"}",
	)
	if err != nil {
		t.Fatalf("CreateRequest() error = %v", err)
	}
	if strings.TrimSpace(record.ID) == "" {
		t.Fatal("CreateRequest() record.ID = empty, want non-empty")
	}

	created := waitRuntimeEvent(t, sub, 2*time.Second)
	if created.Kind != approvalruntime.EventRequestCreated {
		t.Fatalf("created.Kind = %q, want %q", created.Kind, approvalruntime.EventRequestCreated)
	}
	if created.Record == nil || created.Record.ID != record.ID {
		t.Fatalf("created.Record = %#v, want id=%q", created.Record, record.ID)
	}

	pending := runtime.ListPending()
	if len(pending) != 1 || pending[0] == nil || pending[0].ID != record.ID {
		t.Fatalf("runtime.ListPending() = %#v, want one record id=%q", pending, record.ID)
	}

	if err := runtime.Resolve(record.ID, approvalruntime.ApprovalApprove, ""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	decision := waitRuntimeDecision(t, decisionCh, 2*time.Second)
	if decision != approvalruntime.ApprovalApprove {
		t.Fatalf("decision = %v, want %v", decision, approvalruntime.ApprovalApprove)
	}

	resolved := waitRuntimeEvent(t, sub, 2*time.Second)
	if resolved.Kind != approvalruntime.EventRequestResolved {
		t.Fatalf("resolved.Kind = %q, want %q", resolved.Kind, approvalruntime.EventRequestResolved)
	}
	if resolved.Record == nil || resolved.Record.ID != record.ID {
		t.Fatalf("resolved.Record = %#v, want id=%q", resolved.Record, record.ID)
	}
	if resolved.Record.Decision == nil || *resolved.Record.Decision != approvalruntime.ApprovalApprove {
		t.Fatalf("resolved.Record.Decision = %#v, want approve", resolved.Record.Decision)
	}
	if got := runtime.ListPending(); len(got) != 0 {
		t.Fatalf("runtime.ListPending() after resolve len = %d, want 0", len(got))
	}
}

func TestScriptedApprovalCancelBySource(t *testing.T) {
	t.Parallel()

	runtime := approvalruntime.NewApprovalRuntime()

	_, firstCh, err := runtime.CreateRequest(
		context.Background(),
		approvalruntime.ApprovalSource{Kind: approvalruntime.SourceBackgroundAgent, ID: "agent-scripted-1", AgentID: "agent-scripted-1", SubagentType: "planner"},
		"shell",
		"run shell",
	)
	if err != nil {
		t.Fatalf("CreateRequest(first) error = %v", err)
	}

	_, secondCh, err := runtime.CreateRequest(
		context.Background(),
		approvalruntime.ApprovalSource{Kind: approvalruntime.SourceBackgroundAgent, ID: "agent-scripted-1", AgentID: "agent-scripted-1", SubagentType: "planner"},
		"search",
		"run search",
	)
	if err != nil {
		t.Fatalf("CreateRequest(second) error = %v", err)
	}

	thirdRecord, thirdCh, err := runtime.CreateRequest(
		context.Background(),
		approvalruntime.ApprovalSource{Kind: approvalruntime.SourceBackgroundAgent, ID: "agent-scripted-2", AgentID: "agent-scripted-2", SubagentType: "reviewer"},
		"search",
		"other source",
	)
	if err != nil {
		t.Fatalf("CreateRequest(third) error = %v", err)
	}

	canceled := runtime.CancelBySource(approvalruntime.SourceBackgroundAgent, "agent-scripted-1")
	if canceled != 2 {
		t.Fatalf("CancelBySource() = %d, want 2", canceled)
	}

	if decision := waitRuntimeDecision(t, firstCh, 2*time.Second); decision != approvalruntime.ApprovalReject {
		t.Fatalf("first decision = %v, want reject", decision)
	}
	if decision := waitRuntimeDecision(t, secondCh, 2*time.Second); decision != approvalruntime.ApprovalReject {
		t.Fatalf("second decision = %v, want reject", decision)
	}

	select {
	case decision := <-thirdCh:
		t.Fatalf("third decision = %v, want no decision", decision)
	default:
	}

	pending := runtime.ListPending()
	if len(pending) != 1 || pending[0] == nil || pending[0].ID != thirdRecord.ID {
		t.Fatalf("runtime.ListPending() after cancel = %#v, want only third record", pending)
	}
}

func TestScriptedContextCompaction(t *testing.T) {
	t.Parallel()

	ctxStore := soul.NewSoulContext(t.TempDir())
	for _, message := range []soul.Message{
		{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: strings.Repeat("older-user-1 ", 12)}}},
		{Role: soul.RoleAssistant, Content: types.ContentParts{types.TextPart{Text: strings.Repeat("older-assistant-1 ", 8)}}},
		{Role: soul.RoleUser, Content: types.ContentParts{types.TextPart{Text: "recent-user-2"}}},
		{Role: soul.RoleAssistant, Content: types.ContentParts{types.TextPart{Text: "recent-assistant-2"}}},
	} {
		if err := ctxStore.Append(message); err != nil {
			t.Fatalf("SoulContext.Append() error = %v", err)
		}
	}

	provider := &scriptedCompactionProvider{
		streams: [][]llm.ChatEvent{
			{
				{Delta: types.TextPart{Text: "latest assistant reply"}},
				{Done: true},
			},
		},
		summaryText: "scripted compact summary",
	}

	wireCh := make(chan wire.WireMessage, 32)
	engine := soul.NewSoul(provider, ctxStore, nil, wire.ChannelEmitter{Ch: wireCh}, "")
	engine.SetCompactionConfig(soul.CompactionConfig{
		Enabled:        true,
		TriggerRatio:   0.2,
		MaxContextSize: 120,
		ReservedSize:   0,
	})
	engine.SetCompactor(&soul.SimpleCompaction{PreserveLastN: 1})

	if _, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: strings.Repeat("current-user-3 ", 20)},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if provider.ChatCallCount() == 0 {
		t.Fatal("provider.Chat() call count = 0, want >= 1 for compaction")
	}

	messages := ctxStore.Messages()
	if len(messages) != 3 {
		t.Fatalf("context message count after compaction = %d, want 3", len(messages))
	}
	if messages[0].Role != soul.RoleSystem {
		t.Fatalf("messages[0].Role = %q, want system", messages[0].Role)
	}
	if got := strings.TrimSpace(textFromContentParts(messages[0].Content)); got != "scripted compact summary" {
		t.Fatalf("summary message text = %q, want %q", got, "scripted compact summary")
	}
	if !strings.Contains(textFromContentParts(messages[1].Content), "current-user-3") {
		t.Fatalf("messages[1].Content = %q, want contains current-user-3", textFromContentParts(messages[1].Content))
	}
	if !strings.Contains(textFromContentParts(messages[2].Content), "latest assistant reply") {
		t.Fatalf("messages[2].Content = %q, want contains latest assistant reply", textFromContentParts(messages[2].Content))
	}

	events := drainWireMessages(wireCh)
	if !hasCompactionBeginEvent(events) || !hasCompactionEndEvent(events) {
		t.Fatalf("compaction events missing, events=%#v", events)
	}
}

func TestScriptedFetchURL(t *testing.T) {
	t.Parallel()

	const fetchURL = "https://93.184.216.34/scripted-fetch"
	fetchClient := &http.Client{
		Transport: scriptedRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.URL.String(); got != fetchURL {
				t.Fatalf("request URL = %q, want %q", got, fetchURL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/html; charset=utf-8"},
				},
				Body: io.NopCloser(strings.NewReader(`<!doctype html>
<html>
  <body>
    <h1>Scripted Fetch Heading</h1>
    <p>Scripted fetch token 2026</p>
  </body>
</html>`)),
			}, nil
		}),
	}

	ctxStore := soul.NewSoulContext(t.TempDir())
	provider := &scriptedChatProvider{
		streams: [][]llm.ChatEvent{
			{
				{
					ToolCall: &types.ToolCall{
						ID:   "call-fetch-1",
						Name: "fetch_url",
						Arguments: map[string]any{
							"url": fetchURL,
						},
					},
				},
				{Done: true},
			},
			{
				{Delta: types.TextPart{Text: "fetch completed"}},
				{Done: true},
			},
		},
	}

	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(web.NewFetchURL(fetchClient)),
		wire.NoopEmitter{},
		"",
	)

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "fetch url now"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "fetch completed" {
		t.Fatalf("result text = %q, want %q", got, "fetch completed")
	}

	messages := ctxStore.Messages()
	if len(messages) != 4 {
		t.Fatalf("context message count = %d, want 4", len(messages))
	}
	toolOutput := textFromContentParts(messages[2].Content)
	if !strings.Contains(toolOutput, "Scripted Fetch Heading") {
		t.Fatalf("tool output = %q, want contains Scripted Fetch Heading", toolOutput)
	}
	if !strings.Contains(toolOutput, "Scripted fetch token 2026") {
		t.Fatalf("tool output = %q, want contains Scripted fetch token 2026", toolOutput)
	}
}

type scriptedRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn scriptedRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestScriptedPlanModeStateMachine(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	ctxStore := soul.NewSoulContext(t.TempDir())
	state := plan.NewPlanState()
	provider := &scriptedPlanModeProvider{
		planContent: "# Scripted Plan\n- item: implement\n- item: verify\n",
	}

	engine := soul.NewSoul(
		provider,
		ctxStore,
		tools.NewMapToolRegistry(
			plan.NewEnterPlanMode(workDir, state),
			plan.NewExitPlanMode(state),
		),
		wire.NoopEmitter{},
		"",
	)

	result, err := engine.Run(context.Background(), types.ContentParts{
		types.TextPart{Text: "enter plan mode, write plan, then exit with approve"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(textFromContentParts(result.Content)); got != "plan mode flow completed" {
		t.Fatalf("result text = %q, want %q", got, "plan mode flow completed")
	}

	messages := ctxStore.Messages()
	if state.IsActive() {
		t.Fatalf("plan state still active after exit, want inactive; messages=%#v last_plan_file=%q", messages, provider.lastPlanFile)
	}
	if strings.TrimSpace(provider.lastPlanFile) == "" {
		t.Fatal("provider.lastPlanFile = empty, want non-empty")
	}

	if len(messages) != 6 {
		t.Fatalf("context message count = %d, want 6", len(messages))
	}
	enterOutput := strings.TrimSpace(textFromContentParts(messages[2].Content))
	exitOutput := strings.TrimSpace(textFromContentParts(messages[4].Content))
	if !strings.Contains(enterOutput, "plan_file") || !strings.Contains(enterOutput, "active") {
		t.Fatalf("enter tool output = %q, want contains plan_file + active", enterOutput)
	}
	if !strings.Contains(exitOutput, "\"decision\":\"approve\"") {
		t.Fatalf("exit tool output = %q, want contains decision=approve", exitOutput)
	}
	if !strings.Contains(exitOutput, "Scripted Plan") {
		t.Fatalf("exit tool output = %q, want contains plan content", exitOutput)
	}
}

type scriptedCompactionProvider struct {
	streams     [][]llm.ChatEvent
	summaryText string

	mu        sync.Mutex
	streamIdx int
	chatCalls int
}

func (p *scriptedCompactionProvider) ModelName() string {
	return "scripted-compaction"
}

func (p *scriptedCompactionProvider) WithModel(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedCompactionProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedCompactionProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	p.chatCalls++
	p.mu.Unlock()

	return &llm.ChatResponse{
		Content: types.ContentParts{types.TextPart{Text: p.summaryText}},
		Usage: types.TokenUsage{
			InputTokens:  10,
			OutputTokens: 8,
			TotalTokens:  18,
		},
	}, nil
}

func (p *scriptedCompactionProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	p.mu.Lock()
	index := p.streamIdx
	p.streamIdx++
	events := []llm.ChatEvent{{Done: true}}
	if index < len(p.streams) {
		events = p.streams[index]
	}
	p.mu.Unlock()

	ch := make(chan llm.ChatEvent, len(events))
	for i := range events {
		ch <- events[i]
	}
	close(ch)
	return ch, nil
}

func (p *scriptedCompactionProvider) ChatCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.chatCalls
}

type scriptedPlanModeProvider struct {
	planContent  string
	lastPlanFile string

	mu    sync.Mutex
	calls int
}

func (p *scriptedPlanModeProvider) ModelName() string {
	return "scripted-plan-mode"
}

func (p *scriptedPlanModeProvider) WithModel(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedPlanModeProvider) WithThinking(_ string) llm.ChatProvider {
	return p
}

func (p *scriptedPlanModeProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *scriptedPlanModeProvider) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	p.mu.Lock()
	step := p.calls
	p.calls++
	p.mu.Unlock()

	var events []llm.ChatEvent
	switch step {
	case 0:
		events = []llm.ChatEvent{
			{
				ToolCall: &types.ToolCall{
					ID:        "call-enter-plan",
					Name:      "enter_plan_mode",
					Arguments: map[string]any{},
				},
			},
			{Done: true},
		}
	case 1:
		planFile, err := extractPlanFileFromMessages(req.Messages)
		if err == nil {
			if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err == nil {
				if err := os.WriteFile(planFile, []byte(p.planContent), 0o644); err == nil {
					p.mu.Lock()
					p.lastPlanFile = planFile
					p.mu.Unlock()
				}
			}
		}
		events = []llm.ChatEvent{
			{
				ToolCall: &types.ToolCall{
					ID:   "call-exit-plan",
					Name: "exit_plan_mode",
					Arguments: map[string]any{
						"decision": "approve",
						"feedback": "scripted review approved",
					},
				},
			},
			{Done: true},
		}
	default:
		events = []llm.ChatEvent{
			{Delta: types.TextPart{Text: "plan mode flow completed"}},
			{Done: true},
		}
	}

	ch := make(chan llm.ChatEvent, len(events))
	for i := range events {
		ch <- events[i]
	}
	close(ch)
	return ch, nil
}

var planFilePathPattern = regexp.MustCompile(`(/[^\s"'\\]+\.kimi/plans/[a-z0-9-]+\.md)`)

func extractPlanFileFromMessages(messages []llm.Message) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		text := strings.TrimSpace(textFromContentParts(messages[i].Content))
		if text == "" {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			if match := planFilePathPattern.FindString(text); match != "" {
				return strings.TrimSpace(match), nil
			}
			continue
		}
		planFile, _ := payload["plan_file"].(string)
		planFile = strings.TrimSpace(planFile)
		if planFile != "" {
			return planFile, nil
		}
		if match := planFilePathPattern.FindString(text); match != "" {
			return strings.TrimSpace(match), nil
		}
	}
	return "", os.ErrNotExist
}

func hasCompactionBeginEvent(events []wire.WireMessage) bool {
	for i := range events {
		if _, ok := events[i].(wire.CompactionBegin); ok {
			return true
		}
	}
	return false
}

func hasCompactionEndEvent(events []wire.WireMessage) bool {
	for i := range events {
		if _, ok := events[i].(wire.CompactionEnd); ok {
			return true
		}
	}
	return false
}

func waitRuntimeEvent(t *testing.T, ch <-chan approvalruntime.Event, timeout time.Duration) approvalruntime.Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(timeout):
		t.Fatal("timeout waiting approval runtime event")
		return approvalruntime.Event{}
	}
}

func waitRuntimeDecision(t *testing.T, ch <-chan approvalruntime.ApprovalDecision, timeout time.Duration) approvalruntime.ApprovalDecision {
	t.Helper()
	select {
	case decision := <-ch:
		return decision
	case <-time.After(timeout):
		t.Fatal("timeout waiting approval runtime decision")
		return approvalruntime.ApprovalReject
	}
}
