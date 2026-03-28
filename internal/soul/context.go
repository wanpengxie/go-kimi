package soul

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	roleUsage           = "_usage"
	contextJSONLFile    = "context.jsonl"
	contextArchiveStamp = "20060102T150405.000000000Z"
)

// Role identifies who produced one context message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// Message is one logical context message.
type Message struct {
	Role       Role               `json:"role"`
	Content    types.ContentParts `json:"content"`
	ToolCalls  []types.ToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

// SoulContext manages soul message history and token usage persistence.
type SoulContext struct {
	dir        string
	messages   []Message
	tokenCount int64
}

type contextRecord struct {
	Role       string             `json:"role"`
	Content    types.ContentParts `json:"content,omitempty"`
	ToolCalls  []types.ToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	TokenCount int64              `json:"token_count,omitempty"`
}

// NewSoulContext creates a context manager rooted at dir.
func NewSoulContext(dir string) *SoulContext {
	return &SoulContext{
		dir:      dir,
		messages: []Message{},
	}
}

// Restore loads in-memory state from the JSONL history file.
func (c *SoulContext) Restore() error {
	if c == nil {
		return errors.New("soul context: nil")
	}

	path, err := c.filePath()
	if err != nil {
		return err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		c.messages = []Message{}
		c.tokenCount = 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("soul context: open %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := newContextScanner(file)
	lineNo := 0
	messages := make([]Message, 0)
	var tokenCount int64

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record contextRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return fmt.Errorf("soul context: parse line %d: %w", lineNo, err)
		}

		switch record.Role {
		case roleUsage:
			tokenCount = record.TokenCount
		case string(RoleUser), string(RoleAssistant), string(RoleTool), string(RoleSystem):
			messages = append(messages, Message{
				Role:       Role(record.Role),
				Content:    record.Content,
				ToolCalls:  record.ToolCalls,
				ToolCallID: record.ToolCallID,
			})
		case "":
			return fmt.Errorf("soul context: parse line %d: missing role", lineNo)
		default:
			return fmt.Errorf("soul context: parse line %d: unknown role %q", lineNo, record.Role)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("soul context: scan: %w", err)
	}

	c.messages = messages
	c.tokenCount = tokenCount
	return nil
}

// Append appends one message both in memory and on disk.
func (c *SoulContext) Append(m Message) error {
	if c == nil {
		return errors.New("soul context: nil")
	}
	if err := validateMessage(m); err != nil {
		return err
	}

	path, err := c.filePath()
	if err != nil {
		return err
	}

	if err := appendRecord(path, contextRecord{
		Role:       string(m.Role),
		Content:    m.Content,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	}); err != nil {
		return err
	}

	c.messages = append(c.messages, m)
	return nil
}

// Messages returns the current in-memory history snapshot.
func (c *SoulContext) Messages() []Message {
	if c == nil {
		return nil
	}
	out := make([]Message, len(c.messages))
	copy(out, c.messages)
	return out
}

// TokenCount returns the current token usage counter.
func (c *SoulContext) TokenCount() int64 {
	if c == nil {
		return 0
	}
	return c.tokenCount
}

// UpdateTokenCount updates the token counter and appends one usage line.
func (c *SoulContext) UpdateTokenCount(n int64) {
	if c == nil {
		return
	}
	c.tokenCount = n

	path, err := c.filePath()
	if err != nil {
		return
	}
	_ = appendRecord(path, contextRecord{
		Role:       roleUsage,
		TokenCount: n,
	})
}

// Clear archives the existing history file and resets in-memory state.
func (c *SoulContext) Clear() error {
	if c == nil {
		return errors.New("soul context: nil")
	}

	path, err := c.filePath()
	if err != nil {
		return err
	}

	c.messages = []Message{}
	c.tokenCount = 0

	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("soul context: stat %q: %w", path, err)
	}

	archivePath, err := buildArchivePath(path)
	if err != nil {
		return err
	}
	if err := os.Rename(path, archivePath); err != nil {
		return fmt.Errorf("soul context: archive %q -> %q: %w", path, archivePath, err)
	}
	return nil
}

// Replace replaces message history and token count both in memory and on disk.
func (c *SoulContext) Replace(messages []Message, tokenCount int64) error {
	if c == nil {
		return errors.New("soul context: nil")
	}

	for i := range messages {
		if err := validateMessage(messages[i]); err != nil {
			return fmt.Errorf("soul context: replace message[%d]: %w", i, err)
		}
	}

	path, err := c.filePath()
	if err != nil {
		return err
	}

	records := make([]contextRecord, 0, len(messages)+1)
	for i := range messages {
		message := messages[i]
		records = append(records, contextRecord{
			Role:       string(message.Role),
			Content:    message.Content,
			ToolCalls:  message.ToolCalls,
			ToolCallID: message.ToolCallID,
		})
	}
	if tokenCount > 0 {
		records = append(records, contextRecord{
			Role:       roleUsage,
			TokenCount: tokenCount,
		})
	}

	if err := writeRecordsAtomically(path, records); err != nil {
		return err
	}

	c.messages = cloneMessages(messages)
	c.tokenCount = tokenCount
	return nil
}

// Checkpoint returns one checkpoint id for the current in-memory history.
func (c *SoulContext) Checkpoint() int {
	if c == nil {
		return 0
	}
	return len(c.messages)
}

// RevertTo rewinds history to one checkpoint id and persists the truncation.
func (c *SoulContext) RevertTo(checkpointID int) error {
	if c == nil {
		return errors.New("soul context: nil")
	}
	if checkpointID < 0 || checkpointID > len(c.messages) {
		return fmt.Errorf("soul context: invalid checkpoint %d", checkpointID)
	}
	if checkpointID == len(c.messages) {
		return nil
	}

	truncated := cloneMessages(c.messages[:checkpointID])
	tokenCount := estimateContextTokens(truncated)
	if err := c.Replace(truncated, tokenCount); err != nil {
		return fmt.Errorf("soul context: revert checkpoint %d: %w", checkpointID, err)
	}
	return nil
}

func (c *SoulContext) filePath() (string, error) {
	if c == nil {
		return "", errors.New("soul context: nil")
	}
	dir := strings.TrimSpace(c.dir)
	if dir == "" {
		return "", errors.New("soul context: dir is empty")
	}
	return filepath.Join(dir, contextJSONLFile), nil
}

func validateMessage(m Message) error {
	switch m.Role {
	case RoleUser, RoleAssistant, RoleTool, RoleSystem:
	default:
		return fmt.Errorf("soul context: invalid role %q", m.Role)
	}

	hasToolCallID := strings.TrimSpace(m.ToolCallID) != ""
	if m.Role == RoleTool && !hasToolCallID {
		return errors.New("soul context: tool message requires tool_call_id")
	}
	if m.Role != RoleTool && hasToolCallID {
		return errors.New("soul context: tool_call_id is only allowed for tool role")
	}
	return nil
}

func appendRecord(path string, record contextRecord) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("soul context: mkdir %q: %w", dir, err)
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("soul context: open %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := writeRecordLine(file, record); err != nil {
		return err
	}
	return nil
}

func writeRecordsAtomically(path string, records []contextRecord) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("soul context: mkdir %q: %w", dir, err)
		}
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("soul context: create temp for %q: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
		_ = tmpFile.Close()
	}()

	for i := range records {
		if err := writeRecordLine(tmpFile, records[i]); err != nil {
			return err
		}
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("soul context: fsync %q: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("soul context: close %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("soul context: rename %q -> %q: %w", tmpPath, path, err)
	}
	cleanup = false
	return nil
}

func writeRecordLine(file *os.File, record contextRecord) error {
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("soul context: marshal record: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("soul context: write %q: %w", file.Name(), err)
	}
	return nil
}

func buildArchivePath(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	timestamp := time.Now().UTC().Format(contextArchiveStamp)

	for i := 0; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%s", prefix, timestamp))
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", candidate, i)
		}
		candidate += ext

		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("soul context: stat archive path %q: %w", candidate, err)
		}
	}
	return "", errors.New("soul context: unable to allocate unique archive filename")
}

func newContextScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return scanner
}
