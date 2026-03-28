package soul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/llm"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	defaultCompactionTriggerRatio = 0.8
	defaultCompactionMaxContext   = 128000
	defaultCompactionReserved     = 4096
	defaultPreserveLastNRounds    = 2
	defaultCompactionInstruction  = "Summarize the conversation history faithfully. Keep key facts, decisions, constraints, unresolved tasks, and user preferences. Be concise and do not invent details."
)

// CompactionResult is one context compaction output.
type CompactionResult struct {
	Messages []Message
	Usage    *types.TokenUsage
}

// Compactor compacts a message history into a shorter history.
type Compactor interface {
	Compact(ctx context.Context, messages []Message, provider llm.ChatProvider) (CompactionResult, error)
}

// CompactionConfig defines automatic compaction behavior.
type CompactionConfig struct {
	Enabled           bool
	TriggerRatio      float64
	MaxContextSize    int
	ReservedSize      int
	CustomInstruction string
	IncludeThinkParts bool
}

// SimpleCompaction keeps recent rounds and summarizes older messages via provider.Chat.
type SimpleCompaction struct {
	PreserveLastN     int
	Instruction       string
	IncludeThinkParts bool
}

func (c *SimpleCompaction) Compact(ctx context.Context, messages []Message, provider llm.ChatProvider) (CompactionResult, error) {
	if provider == nil {
		return CompactionResult{}, errors.New("soul compaction: nil provider")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	history := cloneMessages(messages)
	if len(history) == 0 {
		return CompactionResult{Messages: nil}, nil
	}

	preserveLastN := defaultPreserveLastNRounds
	if c != nil {
		if c.PreserveLastN > 0 {
			preserveLastN = c.PreserveLastN
		}
		if c.PreserveLastN < 0 {
			preserveLastN = 0
		}
	}

	boundary := compactionBoundary(history, preserveLastN)
	if boundary <= 0 {
		return CompactionResult{
			Messages: history,
		}, nil
	}

	toCompact := history[:boundary]
	preserved := history[boundary:]
	includeThinkParts := false
	if c != nil && c.IncludeThinkParts {
		includeThinkParts = true
	}

	summaryInstruction := strings.TrimSpace(defaultCompactionInstruction)
	if c != nil && strings.TrimSpace(c.Instruction) != "" {
		summaryInstruction = strings.TrimSpace(c.Instruction)
	}

	response, err := provider.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{
				Role: "system",
				Content: types.ContentParts{
					types.TextPart{Text: summaryInstruction},
				},
			},
			{
				Role: "user",
				Content: types.ContentParts{
					types.TextPart{Text: buildCompactionPrompt(toCompact, includeThinkParts)},
				},
			},
		},
	})
	if err != nil {
		return CompactionResult{}, fmt.Errorf("soul compaction: summarize history: %w", err)
	}
	if response == nil {
		return CompactionResult{}, errors.New("soul compaction: nil chat response")
	}

	summaryContent := cloneContentParts(response.Content)
	if len(summaryContent) == 0 {
		return CompactionResult{}, errors.New("soul compaction: empty summary content")
	}

	compacted := make([]Message, 0, 1+len(preserved))
	compacted = append(compacted, Message{
		Role:    RoleSystem,
		Content: summaryContent,
	})
	compacted = append(compacted, cloneMessages(preserved)...)

	return CompactionResult{
		Messages: compacted,
		Usage:    usagePtr(response.Usage),
	}, nil
}

func (s *Soul) postStepCompaction(ctx context.Context) error {
	if s == nil || s.context == nil {
		return nil
	}

	messages := s.context.Messages()
	tokenCount := estimateContextTokens(messages)
	s.context.UpdateTokenCount(tokenCount)

	cfg := normalizeCompactionConfig(s.compaction)
	s.compaction = cfg
	if !cfg.Enabled || s.compactor == nil {
		return nil
	}
	if !shouldAutoCompact(tokenCount, cfg.MaxContextSize, cfg.TriggerRatio, cfg.ReservedSize) {
		return nil
	}

	return s.compactMessages(ctx, "token_limit", messages, cfg)
}

// Compact triggers one manual compaction attempt immediately.
func (s *Soul) Compact(ctx context.Context) error {
	if s == nil {
		return errors.New("soul compaction: nil soul")
	}
	if err := s.ensureReady(); err != nil {
		return err
	}
	if s.context == nil {
		return errors.New("soul compaction: nil context")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	messages := s.context.Messages()
	if len(messages) == 0 {
		return nil
	}
	tokenCount := estimateContextTokens(messages)
	s.context.UpdateTokenCount(tokenCount)

	cfg := normalizeCompactionConfig(s.compaction)
	s.compaction = cfg
	if s.compactor == nil {
		return errors.New("soul compaction: nil compactor")
	}

	return s.compactMessages(ctx, "manual", messages, cfg)
}

func (s *Soul) compactMessages(
	ctx context.Context,
	trigger string,
	messages []Message,
	cfg CompactionConfig,
) error {
	if err := s.emit(wire.CompactionBegin{Trigger: trigger}); err != nil {
		return err
	}

	compactor := s.compactor
	if simpleCompactor, ok := compactor.(*SimpleCompaction); ok && simpleCompactor != nil {
		cloned := *simpleCompactor
		if cfg.CustomInstruction != "" {
			cloned.Instruction = cfg.CustomInstruction
		}
		cloned.IncludeThinkParts = cfg.IncludeThinkParts
		compactor = &cloned
	}

	result, err := compactor.Compact(ctx, messages, s.provider)
	if err != nil {
		return fmt.Errorf("soul compaction: %w", err)
	}
	if len(result.Messages) == 0 {
		return errors.New("soul compaction: empty compacted messages")
	}

	newTokenCount := estimateContextTokens(result.Messages)
	if err := s.context.Replace(result.Messages, newTokenCount); err != nil {
		return fmt.Errorf("soul compaction: replace context: %w", err)
	}

	summary, summaryContent := compactionSummary(result.Messages)
	if err := s.emit(wire.CompactionEnd{
		Summary: summary,
		Content: summaryContent,
	}); err != nil {
		return err
	}

	return nil
}

func defaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Enabled:           true,
		TriggerRatio:      defaultCompactionTriggerRatio,
		MaxContextSize:    defaultCompactionMaxContext,
		ReservedSize:      defaultCompactionReserved,
		IncludeThinkParts: false,
	}
}

func normalizeCompactionConfig(cfg CompactionConfig) CompactionConfig {
	if cfg.TriggerRatio <= 0 {
		cfg.TriggerRatio = defaultCompactionTriggerRatio
	}
	if cfg.MaxContextSize <= 0 {
		cfg.MaxContextSize = defaultCompactionMaxContext
	}
	if cfg.ReservedSize < 0 {
		cfg.ReservedSize = 0
	}
	cfg.CustomInstruction = strings.TrimSpace(cfg.CustomInstruction)
	return cfg
}

func shouldAutoCompact(tokenCount int64, maxContext int, triggerRatio float64, reserved int) bool {
	if tokenCount <= 0 || maxContext <= 0 {
		return false
	}
	if triggerRatio <= 0 {
		triggerRatio = defaultCompactionTriggerRatio
	}
	if reserved < 0 {
		reserved = 0
	}
	if reserved >= maxContext {
		return false
	}

	remaining := maxContext - int(tokenCount)
	if remaining <= reserved {
		return true
	}
	return float64(tokenCount)/float64(maxContext) >= triggerRatio
}

func estimateContextTokens(messages []Message) int64 {
	payload := renderMessagesForCompaction(messages, true)
	if payload == "" {
		return 0
	}
	return int64(len(payload) / 4)
}

func compactionBoundary(messages []Message, preserveLastN int) int {
	if len(messages) == 0 {
		return 0
	}
	if preserveLastN <= 0 {
		return len(messages)
	}

	roundStarts := make([]int, 0, len(messages))
	for i := range messages {
		if messages[i].Role == RoleUser || messages[i].Role == RoleAssistant {
			roundStarts = append(roundStarts, i)
		}
	}
	if len(roundStarts) == 0 || preserveLastN >= len(roundStarts) {
		return 0
	}
	return roundStarts[len(roundStarts)-preserveLastN]
}

func buildCompactionPrompt(messages []Message, includeThinkParts bool) string {
	return "Summarize the following conversation history as context for the next assistant turn.\n" +
		"Keep durable facts, key decisions, constraints, and unresolved tasks.\n\n" +
		renderMessagesForCompaction(messages, includeThinkParts)
}

func renderMessagesForCompaction(messages []Message, includeThinkParts bool) string {
	if len(messages) == 0 {
		return ""
	}

	records := make([]contextRecord, 0, len(messages))
	for i := range messages {
		records = append(records, contextRecord{
			Role:       string(messages[i].Role),
			Content:    filterCompactionContent(messages[i].Content, includeThinkParts),
			ToolCalls:  cloneToolCalls(messages[i].ToolCalls),
			ToolCallID: strings.TrimSpace(messages[i].ToolCallID),
		})
	}

	var builder strings.Builder
	for i := range records {
		line, err := json.Marshal(records[i])
		if err != nil {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.Write(line)
	}
	return builder.String()
}

func filterCompactionContent(parts types.ContentParts, includeThinkParts bool) types.ContentParts {
	if len(parts) == 0 {
		return nil
	}
	out := make(types.ContentParts, 0, len(parts))
	for i := range parts {
		switch parts[i].(type) {
		case types.ThinkPart, *types.ThinkPart:
			if !includeThinkParts {
				continue
			}
		}
		out = append(out, parts[i])
	}
	if len(out) == 0 {
		return nil
	}
	return cloneContentParts(out)
}

func compactionSummary(messages []Message) (string, types.ContentParts) {
	for i := range messages {
		if messages[i].Role != RoleSystem {
			continue
		}
		content := cloneContentParts(messages[i].Content)
		return strings.TrimSpace(contentPartsText(content)), content
	}
	if len(messages) == 0 {
		return "", nil
	}
	content := cloneContentParts(messages[0].Content)
	return strings.TrimSpace(contentPartsText(content)), content
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i := range messages {
		out[i] = Message{
			Role:       messages[i].Role,
			Content:    cloneContentParts(messages[i].Content),
			ToolCalls:  cloneToolCalls(messages[i].ToolCalls),
			ToolCallID: strings.TrimSpace(messages[i].ToolCallID),
		}
	}
	return out
}

func contentPartsText(parts types.ContentParts) string {
	if len(parts) == 0 {
		return ""
	}
	var builder strings.Builder
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			builder.WriteString(typed.Text)
		case *types.TextPart:
			if typed != nil {
				builder.WriteString(typed.Text)
			}
		}
	}
	return builder.String()
}
