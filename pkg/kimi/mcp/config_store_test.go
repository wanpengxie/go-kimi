package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMCPConfigStoreSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "mcp.json")
	input := []MCPServerConfig{
		{
			Name:      " fs ",
			Transport: TransportType(" STDIO "),
			Command:   " npx ",
			Args:      []string{"-y", "server-filesystem"},
			Env:       map[string]string{"NODE_ENV": "test"},
		},
		{
			Name:      "search",
			Transport: TransportSSE,
			URL:       " https://example.test/mcp ",
			Headers:   map[string]string{"Authorization": "Bearer token"},
		},
	}
	if err := SaveMCPConfig(path, input); err != nil {
		t.Fatalf("SaveMCPConfig() error = %v", err)
	}

	loaded, err := LoadMCPConfig(path)
	if err != nil {
		t.Fatalf("LoadMCPConfig() error = %v", err)
	}

	want := []MCPServerConfig{
		{
			Name:      "fs",
			Transport: TransportStdio,
			Command:   "npx",
			Args:      []string{"-y", "server-filesystem"},
			Env:       map[string]string{"NODE_ENV": "test"},
		},
		{
			Name:      "search",
			Transport: TransportSSE,
			URL:       "https://example.test/mcp",
			Headers:   map[string]string{"Authorization": "Bearer token"},
		},
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("LoadMCPConfig() = %#v, want %#v", loaded, want)
	}
}

func TestMCPConfigStoreLoadMissingReturnsEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	loaded, err := LoadMCPConfig(path)
	if err != nil {
		t.Fatalf("LoadMCPConfig(missing) error = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("LoadMCPConfig(missing) len = %d, want 0", len(loaded))
	}
}

func TestMCPConfigStoreAddListRemove(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.json")

	if err := AddServer(path, MCPServerConfig{
		Name:      "fs",
		Transport: TransportStdio,
		Command:   "npx",
	}); err != nil {
		t.Fatalf("AddServer(fs) error = %v", err)
	}

	if err := AddServer(path, MCPServerConfig{
		Name:      "search",
		Transport: TransportSSE,
		URL:       "https://example.test/mcp",
	}); err != nil {
		t.Fatalf("AddServer(search) error = %v", err)
	}

	servers, err := ListServers(path)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("len(ListServers) = %d, want 2", len(servers))
	}

	if err := AddServer(path, MCPServerConfig{
		Name:      "FS",
		Transport: TransportStdio,
		Command:   "npx",
	}); err == nil {
		t.Fatal("AddServer(duplicate) error = nil, want duplicate error")
	}

	if err := RemoveServer(path, " FS "); err != nil {
		t.Fatalf("RemoveServer(fs) error = %v", err)
	}

	servers, err = ListServers(path)
	if err != nil {
		t.Fatalf("ListServers(after remove) error = %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "search" {
		t.Fatalf("servers after remove = %#v, want only search", servers)
	}

	if err := RemoveServer(path, "missing"); err == nil {
		t.Fatal("RemoveServer(missing) error = nil, want not found error")
	}
}

func TestMCPConfigStoreDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	input := []MCPServerConfig{
		{Name: "fs", Transport: TransportStdio, Command: "npx"},
	}
	if err := SaveMCPConfig("", input); err != nil {
		t.Fatalf("SaveMCPConfig(default path) error = %v", err)
	}

	defaultPath := filepath.Join(home, ".kimi", "mcp.json")
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatalf("os.Stat(default path) error = %v", err)
	}

	loaded, err := LoadMCPConfig("")
	if err != nil {
		t.Fatalf("LoadMCPConfig(default path) error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "fs" {
		t.Fatalf("LoadMCPConfig(default path) = %#v, want one fs server", loaded)
	}
}

func TestMCPConfigStoreErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := LoadMCPConfig(path); err == nil {
		t.Fatal("LoadMCPConfig(invalid json) error = nil, want error")
	}

	if err := RemoveServer(path, " "); err == nil {
		t.Fatal("RemoveServer(empty name) error = nil, want error")
	}
	if err := RemoveServer(path, "a"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("RemoveServer(on invalid file) error = %v, want decode error", err)
	}
}
