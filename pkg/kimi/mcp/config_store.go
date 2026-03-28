package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMCPConfigDirName  = ".kimi"
	defaultMCPConfigFileName = "mcp.json"
	mcpConfigDirPerm         = 0o700
	mcpConfigFilePerm        = 0o600
)

// LoadMCPConfig loads MCP server configs from a json file.
// Empty path uses default ~/.kimi/mcp.json.
func LoadMCPConfig(path string) ([]MCPServerConfig, error) {
	resolvedPath, err := resolveMCPConfigPath(path)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []MCPServerConfig{}, nil
		}
		return nil, fmt.Errorf("mcp config store: read %q: %w", resolvedPath, err)
	}

	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return []MCPServerConfig{}, nil
	}

	var configs []MCPServerConfig
	if err := json.Unmarshal(content, &configs); err != nil {
		return nil, fmt.Errorf("mcp config store: decode %q: %w", resolvedPath, err)
	}

	return normalizeConfigList(configs)
}

// SaveMCPConfig validates and saves configs to one json file.
// Empty path uses default ~/.kimi/mcp.json.
func SaveMCPConfig(path string, configs []MCPServerConfig) error {
	resolvedPath, err := resolveMCPConfigPath(path)
	if err != nil {
		return err
	}

	normalized, err := normalizeConfigList(configs)
	if err != nil {
		return err
	}

	payload, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp config store: encode %q: %w", resolvedPath, err)
	}
	payload = append(payload, '\n')

	dir := filepath.Dir(resolvedPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, mcpConfigDirPerm); err != nil {
			return fmt.Errorf("mcp config store: mkdir %q: %w", dir, err)
		}
	}
	if err := writeFileAtomic(resolvedPath, payload, mcpConfigFilePerm); err != nil {
		return fmt.Errorf("mcp config store: write %q: %w", resolvedPath, err)
	}
	return nil
}

// AddServer appends one server config into store.
func AddServer(path string, cfg MCPServerConfig) error {
	configs, err := LoadMCPConfig(path)
	if err != nil {
		return err
	}

	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return err
	}

	for i := range configs {
		if strings.EqualFold(configs[i].Name, normalized.Name) {
			return fmt.Errorf("mcp config store: server %q already exists", normalized.Name)
		}
	}

	configs = append(configs, normalized)
	return SaveMCPConfig(path, configs)
}

// RemoveServer removes one server config by name.
func RemoveServer(path string, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("mcp config store: server name is required")
	}

	configs, err := LoadMCPConfig(path)
	if err != nil {
		return err
	}

	filtered := make([]MCPServerConfig, 0, len(configs))
	removed := false
	for i := range configs {
		if strings.EqualFold(configs[i].Name, name) {
			removed = true
			continue
		}
		filtered = append(filtered, configs[i])
	}
	if !removed {
		return fmt.Errorf("mcp config store: server %q not found", name)
	}

	return SaveMCPConfig(path, filtered)
}

// ListServers returns all MCP server configs in store.
func ListServers(path string) ([]MCPServerConfig, error) {
	return LoadMCPConfig(path)
}

func writeFileAtomic(path string, payload []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %q: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		_ = tmpFile.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp %q: %w", tmpPath, err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		return fmt.Errorf("write temp %q: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("fsync temp %q: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, path, err)
	}

	cleanup = false
	return nil
}

func resolveMCPConfigPath(path string) (string, error) {
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		return trimmed, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("mcp config store: resolve home dir: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("mcp config store: home dir is empty")
	}

	return filepath.Join(home, defaultMCPConfigDirName, defaultMCPConfigFileName), nil
}

func normalizeConfigList(configs []MCPServerConfig) ([]MCPServerConfig, error) {
	normalized := make([]MCPServerConfig, 0, len(configs))
	seen := make(map[string]struct{}, len(configs))

	for i := range configs {
		cfg, err := normalizeServerConfig(configs[i])
		if err != nil {
			return nil, fmt.Errorf("mcp config store: configs[%d]: %w", i, err)
		}

		key := strings.ToLower(cfg.Name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("mcp config store: duplicate server name %q", cfg.Name)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, cfg)
	}

	return normalized, nil
}
