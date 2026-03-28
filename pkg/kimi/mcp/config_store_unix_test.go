//go:build unix

package mcp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMCPConfigStoreSavePermissionsUnix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "mcp.json")
	configs := []MCPServerConfig{
		{
			Name:      "search",
			Transport: TransportSSE,
			URL:       "https://example.test/mcp",
			Headers:   map[string]string{"Authorization": "Bearer token"},
		},
	}

	if err := SaveMCPConfig(path, configs); err != nil {
		t.Fatalf("SaveMCPConfig() error = %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(file) error = %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != mcpConfigFilePerm {
		t.Fatalf("file mode = %04o, want %04o", got, mcpConfigFilePerm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("os.Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != mcpConfigDirPerm {
		t.Fatalf("dir mode = %04o, want %04o", got, mcpConfigDirPerm)
	}
}

func TestMCPConfigStoreSaveAtomicReplaceUnix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.json")
	initial := []MCPServerConfig{
		{
			Name:      "fs",
			Transport: TransportStdio,
			Command:   "npx",
			Args:      []string{"-y", "server-filesystem"},
		},
	}
	if err := SaveMCPConfig(path, initial); err != nil {
		t.Fatalf("SaveMCPConfig(initial) error = %v", err)
	}

	oldFD, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(initial) error = %v", err)
	}
	defer func() {
		_ = oldFD.Close()
	}()

	oldPayload, err := io.ReadAll(oldFD)
	if err != nil {
		t.Fatalf("io.ReadAll(initial fd) error = %v", err)
	}

	updated := []MCPServerConfig{
		{
			Name:      "search",
			Transport: TransportSSE,
			URL:       "https://example.test/mcp",
			Headers:   map[string]string{"Authorization": "Bearer new-token"},
		},
	}
	if err := SaveMCPConfig(path, updated); err != nil {
		t.Fatalf("SaveMCPConfig(updated) error = %v", err)
	}

	if _, err := oldFD.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("oldFD.Seek(0) error = %v", err)
	}
	stalePayload, err := io.ReadAll(oldFD)
	if err != nil {
		t.Fatalf("io.ReadAll(stale fd) error = %v", err)
	}
	if !bytes.Equal(stalePayload, oldPayload) {
		t.Fatalf("stale fd content changed; write is not replace-via-rename")
	}

	freshPayload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(fresh path) error = %v", err)
	}
	if bytes.Equal(freshPayload, oldPayload) {
		t.Fatalf("fresh path content did not change")
	}

	loaded, err := LoadMCPConfig(path)
	if err != nil {
		t.Fatalf("LoadMCPConfig(updated) error = %v", err)
	}
	if !reflect.DeepEqual(loaded, updated) {
		t.Fatalf("LoadMCPConfig(updated) = %#v, want %#v", loaded, updated)
	}
}
