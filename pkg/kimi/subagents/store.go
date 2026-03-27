package subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	metaFileName    = "meta.json"
	contextFileName = "context.jsonl"
	wireFileName    = "wire.jsonl"
	promptFileName  = "prompt.txt"
)

// SubagentStore persists subagent instance records under one session directory.
type SubagentStore struct {
	dir string
}

// NewSubagentStore creates one store rooted at session/subagents/.
func NewSubagentStore(dir string) *SubagentStore {
	return &SubagentStore{dir: dir}
}

// Create creates one subagent instance directory and writes its metadata.
func (s *SubagentStore) Create(record *AgentInstanceRecord) error {
	normalized, err := normalizeRecord(record)
	if err != nil {
		return err
	}

	baseDir, err := s.storeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("subagents: mkdir %q: %w", baseDir, err)
	}

	agentDir := filepath.Join(baseDir, normalized.AgentID)
	if err := os.Mkdir(agentDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("subagents: create %q: %w", normalized.AgentID, os.ErrExist)
		}
		return fmt.Errorf("subagents: mkdir %q: %w", agentDir, err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(agentDir)
		}
	}()

	metaPath := filepath.Join(agentDir, metaFileName)
	if err := writeRecord(metaPath, normalized); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(agentDir, contextFileName), nil, 0o644); err != nil {
		return fmt.Errorf("subagents: write %q: %w", filepath.Join(agentDir, contextFileName), err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, wireFileName), nil, 0o644); err != nil {
		return fmt.Errorf("subagents: write %q: %w", filepath.Join(agentDir, wireFileName), err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, promptFileName), []byte(normalized.Description), 0o644); err != nil {
		return fmt.Errorf("subagents: write %q: %w", filepath.Join(agentDir, promptFileName), err)
	}

	cleanup = false
	return nil
}

// Get reads one subagent instance metadata record by agent id.
func (s *SubagentStore) Get(agentID string) (*AgentInstanceRecord, error) {
	metaPath, err := s.metaPath(agentID)
	if err != nil {
		return nil, err
	}

	record, err := readRecord(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("subagents: agent %q not found: %w", strings.TrimSpace(agentID), os.ErrNotExist)
		}
		return nil, err
	}
	return record, nil
}

// Update rewrites one existing subagent instance metadata record.
func (s *SubagentStore) Update(record *AgentInstanceRecord) error {
	normalized, err := normalizeRecord(record)
	if err != nil {
		return err
	}

	metaPath, err := s.metaPath(normalized.AgentID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(metaPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("subagents: agent %q not found: %w", normalized.AgentID, os.ErrNotExist)
		}
		return fmt.Errorf("subagents: stat %q: %w", metaPath, err)
	}

	return writeRecord(metaPath, normalized)
}

// List returns all persisted subagent instance records.
func (s *SubagentStore) List() ([]*AgentInstanceRecord, error) {
	baseDir, err := s.storeDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("subagents: read dir %q: %w", baseDir, err)
	}

	records := make([]*AgentInstanceRecord, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(baseDir, entry.Name(), metaFileName)
		record, err := readRecord(metaPath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].AgentID < records[j].AgentID
	})
	return records, nil
}

// Delete removes one subagent instance directory.
func (s *SubagentStore) Delete(agentID string) error {
	baseDir, err := s.storeDir()
	if err != nil {
		return err
	}
	normalizedAgentID, err := normalizeAgentID(agentID)
	if err != nil {
		return err
	}

	agentDir := filepath.Join(baseDir, normalizedAgentID)
	if _, err := os.Stat(agentDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("subagents: agent %q not found: %w", normalizedAgentID, os.ErrNotExist)
		}
		return fmt.Errorf("subagents: stat %q: %w", agentDir, err)
	}
	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("subagents: delete %q: %w", normalizedAgentID, err)
	}
	return nil
}

func (s *SubagentStore) storeDir() (string, error) {
	if s == nil {
		return "", errors.New("subagents: nil store")
	}

	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return "", errors.New("subagents: store dir is empty")
	}
	return filepath.Clean(dir), nil
}

func (s *SubagentStore) metaPath(agentID string) (string, error) {
	baseDir, err := s.storeDir()
	if err != nil {
		return "", err
	}

	normalizedAgentID, err := normalizeAgentID(agentID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, normalizedAgentID, metaFileName), nil
}

func normalizeRecord(record *AgentInstanceRecord) (*AgentInstanceRecord, error) {
	if record == nil {
		return nil, errors.New("subagents: record is nil")
	}

	normalizedAgentID, err := normalizeAgentID(record.AgentID)
	if err != nil {
		return nil, err
	}

	normalized := *record
	normalized.AgentID = normalizedAgentID
	normalized.SubagentType = strings.TrimSpace(normalized.SubagentType)
	normalized.LastTaskID = strings.TrimSpace(normalized.LastTaskID)
	normalized.LaunchSpec.AgentID = strings.TrimSpace(normalized.LaunchSpec.AgentID)
	normalized.LaunchSpec.SubagentType = strings.TrimSpace(normalized.LaunchSpec.SubagentType)
	normalized.LaunchSpec.ModelOverride = strings.TrimSpace(normalized.LaunchSpec.ModelOverride)
	normalized.LaunchSpec.EffectiveModel = strings.TrimSpace(normalized.LaunchSpec.EffectiveModel)

	if normalized.SubagentType == "" {
		return nil, errors.New("subagents: subagent_type is required")
	}
	if normalized.Status == "" {
		normalized.Status = StatusIdle
	}
	if normalized.LaunchSpec.AgentID == "" {
		normalized.LaunchSpec.AgentID = normalized.AgentID
	} else if normalized.LaunchSpec.AgentID != normalized.AgentID {
		return nil, errors.New("subagents: launch_spec.agent_id must equal agent_id")
	}
	if normalized.LaunchSpec.SubagentType == "" {
		normalized.LaunchSpec.SubagentType = normalized.SubagentType
	}
	return &normalized, nil
}

func normalizeAgentID(agentID string) (string, error) {
	normalized := strings.TrimSpace(agentID)
	if normalized == "" {
		return "", errors.New("subagents: agent_id is required")
	}
	if normalized == "." || normalized == ".." {
		return "", fmt.Errorf("subagents: invalid agent_id %q", normalized)
	}
	if strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") {
		return "", fmt.Errorf("subagents: invalid agent_id %q", normalized)
	}
	return normalized, nil
}

func readRecord(path string) (*AgentInstanceRecord, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("subagents: read %q: %w", path, err)
	}

	var record AgentInstanceRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("subagents: decode %q: %w", path, err)
	}

	normalized, err := normalizeRecord(&record)
	if err != nil {
		return nil, fmt.Errorf("subagents: invalid record %q: %w", path, err)
	}
	return normalized, nil
}

func writeRecord(path string, record *AgentInstanceRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("subagents: marshal record: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("subagents: write %q: %w", path, err)
	}
	return nil
}
