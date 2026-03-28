package mcp

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeServerConfigSuccess(t *testing.T) {
	t.Parallel()

	in := MCPServerConfig{
		Name:      "  Filesystem  ",
		Transport: TransportType(" STDIO "),
		Command:   "  npx  ",
		Args:      []string{"-y", "server"},
		Env: map[string]string{
			"NODE_ENV": "test",
		},
	}
	out, err := normalizeServerConfig(in)
	if err != nil {
		t.Fatalf("normalizeServerConfig() error = %v", err)
	}

	if out.Name != "Filesystem" {
		t.Fatalf("Name = %q, want %q", out.Name, "Filesystem")
	}
	if out.Transport != TransportStdio {
		t.Fatalf("Transport = %q, want %q", out.Transport, TransportStdio)
	}
	if out.Command != "npx" {
		t.Fatalf("Command = %q, want %q", out.Command, "npx")
	}

	in.Args[0] = "mutated"
	in.Env["NODE_ENV"] = "prod"
	if out.Args[0] != "-y" {
		t.Fatalf("Args should be cloned, got %#v", out.Args)
	}
	if out.Env["NODE_ENV"] != "test" {
		t.Fatalf("Env should be cloned, got %#v", out.Env)
	}
}

func TestNormalizeServerConfigErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     MCPServerConfig
		wantSubstr string
	}{
		{
			name:       "missing name",
			config:     MCPServerConfig{Transport: TransportStdio, Command: "npx"},
			wantSubstr: "server name is required",
		},
		{
			name:       "unsupported transport",
			config:     MCPServerConfig{Name: "a", Transport: TransportType("http")},
			wantSubstr: "unsupported transport",
		},
		{
			name:       "stdio missing command",
			config:     MCPServerConfig{Name: "a", Transport: TransportStdio},
			wantSubstr: "command is required",
		},
		{
			name:       "sse missing url",
			config:     MCPServerConfig{Name: "a", Transport: TransportSSE},
			wantSubstr: "url is required",
		},
		{
			name: "env empty key",
			config: MCPServerConfig{
				Name:      "a",
				Transport: TransportStdio,
				Command:   "cmd",
				Env: map[string]string{
					"": "bad",
				},
			},
			wantSubstr: "env contains empty key",
		},
		{
			name: "headers empty key",
			config: MCPServerConfig{
				Name:      "a",
				Transport: TransportSSE,
				URL:       "http://localhost",
				Headers: map[string]string{
					" ": "bad",
				},
			},
			wantSubstr: "headers contains empty key",
		},
		{
			name: "negative timeout",
			config: MCPServerConfig{
				Name:           "a",
				Transport:      TransportStdio,
				Command:        "cmd",
				TimeoutSeconds: -1,
			},
			wantSubstr: "timeout_seconds must be >= 0",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := normalizeServerConfig(tc.config)
			if err == nil {
				t.Fatal("normalizeServerConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("normalizeServerConfig() error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestNormalizeConfigListRejectsDuplicateServerNameCaseInsensitive(t *testing.T) {
	t.Parallel()

	_, err := normalizeConfigList([]MCPServerConfig{
		{Name: "FS", Transport: TransportStdio, Command: "cmd"},
		{Name: "fs", Transport: TransportSSE, URL: "http://localhost"},
	})
	if err == nil {
		t.Fatal("normalizeConfigList() error = nil, want duplicate error")
	}
	if !strings.Contains(err.Error(), "duplicate server name") {
		t.Fatalf("normalizeConfigList() error = %q, want duplicate server name", err.Error())
	}
}

func TestNormalizeTransportType(t *testing.T) {
	t.Parallel()

	if got := normalizeTransportType(TransportType(" SSE ")); got != TransportSSE {
		t.Fatalf("normalizeTransportType() = %q, want %q", got, TransportSSE)
	}
	if got := normalizeTransportType(TransportType(" streamable-http ")); got != TransportStreamableHTTP {
		t.Fatalf("normalizeTransportType(streamable-http) = %q, want %q", got, TransportStreamableHTTP)
	}
	if got := normalizeTransportType(TransportType(" STREAMABLE_HTTP ")); got != TransportStreamableHTTP {
		t.Fatalf("normalizeTransportType(streamable_http) = %q, want %q", got, TransportStreamableHTTP)
	}
	if got := normalizeTransportType(TransportType("")); got != TransportType("") {
		t.Fatalf("normalizeTransportType(empty) = %q, want empty", got)
	}
}

func TestNormalizeConfigListSuccess(t *testing.T) {
	t.Parallel()

	list, err := normalizeConfigList([]MCPServerConfig{
		{Name: "fs", Transport: TransportStdio, Command: "cmd"},
		{Name: "http", Transport: TransportSSE, URL: "http://localhost"},
	})
	if err != nil {
		t.Fatalf("normalizeConfigList() error = %v", err)
	}

	want := []MCPServerConfig{
		{Name: "fs", Transport: TransportStdio, Command: "cmd"},
		{Name: "http", Transport: TransportSSE, URL: "http://localhost"},
	}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("normalizeConfigList() = %#v, want %#v", list, want)
	}
}
