package agentspec

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAgentSpecNormalizesFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	content := `
name: "  support-agent  "
system_prompt: "  be concise  "
model: "  kimi-k2  "
tools:
  allow: [" shell ", "shell", "read_file"]
  exclude: [" write_file ", ""]
subagent_types: [" planner ", "planner", "executor"]
`
	if err := osWriteFile(path, content); err != nil {
		t.Fatalf("osWriteFile() error = %v", err)
	}

	spec, err := LoadAgentSpec(path)
	if err != nil {
		t.Fatalf("LoadAgentSpec() error = %v", err)
	}

	if spec.Name != "support-agent" {
		t.Fatalf("Name = %q, want support-agent", spec.Name)
	}
	if spec.SystemPrompt != "be concise" {
		t.Fatalf("SystemPrompt = %q, want be concise", spec.SystemPrompt)
	}
	if spec.Model != "kimi-k2" {
		t.Fatalf("Model = %q, want kimi-k2", spec.Model)
	}
	if !reflect.DeepEqual(spec.Tools.AllowedTools, []string{"shell", "read_file"}) {
		t.Fatalf("AllowedTools = %#v", spec.Tools.AllowedTools)
	}
	if !reflect.DeepEqual(spec.Tools.ExcludedTools, []string{"write_file"}) {
		t.Fatalf("ExcludedTools = %#v", spec.Tools.ExcludedTools)
	}
	if !reflect.DeepEqual(spec.SubagentTypes, []string{"planner", "executor"}) {
		t.Fatalf("SubagentTypes = %#v", spec.SubagentTypes)
	}
}

func TestResolveAgentSpecInheritance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	parentPath := filepath.Join(dir, "base.yaml")
	childPath := filepath.Join(dir, "child.yaml")

	if err := osWriteFile(parentPath, `
name: base-agent
system_prompt: parent prompt
model: model-a
tools:
  allowed_tools: ["read_file"]
  excluded_tools: ["write_file"]
subagent_types: ["planner"]
`); err != nil {
		t.Fatalf("write parent error = %v", err)
	}

	if err := osWriteFile(childPath, `
name: child-agent
extends: ./base.yaml
model: model-b
tools:
  allow: ["shell"]
  exclude: ["fetch_url"]
subagent_types: ["executor"]
`); err != nil {
		t.Fatalf("write child error = %v", err)
	}

	resolved, err := ResolveAgentSpec(childPath)
	if err != nil {
		t.Fatalf("ResolveAgentSpec() error = %v", err)
	}

	if resolved.Name != "child-agent" {
		t.Fatalf("Name = %q, want child-agent", resolved.Name)
	}
	if resolved.SystemPrompt != "parent prompt" {
		t.Fatalf("SystemPrompt = %q, want parent prompt", resolved.SystemPrompt)
	}
	if resolved.Model != "model-b" {
		t.Fatalf("Model = %q, want model-b", resolved.Model)
	}
	if !reflect.DeepEqual(resolved.AllowedTools, []string{"read_file", "shell"}) {
		t.Fatalf("AllowedTools = %#v", resolved.AllowedTools)
	}
	if !reflect.DeepEqual(resolved.ExcludedTools, []string{"write_file", "fetch_url"}) {
		t.Fatalf("ExcludedTools = %#v", resolved.ExcludedTools)
	}
	if !reflect.DeepEqual(resolved.SubagentTypes, []string{"planner", "executor"}) {
		t.Fatalf("SubagentTypes = %#v", resolved.SubagentTypes)
	}
	if len(resolved.InheritanceChain) != 2 {
		t.Fatalf("InheritanceChain len = %d, want 2", len(resolved.InheritanceChain))
	}
}

func TestResolveAgentSpecCycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.yaml")
	bPath := filepath.Join(dir, "b.yaml")
	if err := osWriteFile(aPath, "name: a\nextends: ./b.yaml\n"); err != nil {
		t.Fatalf("write a error = %v", err)
	}
	if err := osWriteFile(bPath, "name: b\nextends: ./a.yaml\n"); err != nil {
		t.Fatalf("write b error = %v", err)
	}

	_, err := ResolveAgentSpec(aPath)
	if err == nil {
		t.Fatal("ResolveAgentSpec() error = nil, want cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("ResolveAgentSpec() error = %v, want contains cycle", err)
	}
}

func TestResolveAgentSpecConflictingTools(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	parentPath := filepath.Join(dir, "base.yaml")
	childPath := filepath.Join(dir, "child.yaml")
	if err := osWriteFile(parentPath, "name: base\ntools:\n  allowed_tools: [\"shell\"]\n"); err != nil {
		t.Fatalf("write parent error = %v", err)
	}
	if err := osWriteFile(childPath, "name: child\nextends: ./base.yaml\ntools:\n  excluded_tools: [\"shell\"]\n"); err != nil {
		t.Fatalf("write child error = %v", err)
	}

	_, err := ResolveAgentSpec(childPath)
	if err == nil {
		t.Fatal("ResolveAgentSpec() error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), "allow/exclude") {
		t.Fatalf("ResolveAgentSpec() error = %v, want allow/exclude", err)
	}
}

func TestResolveAgentSpecRejectsExtendsPathTraversal(t *testing.T) {
	t.Parallel()

	sandbox := t.TempDir()
	rootDir := filepath.Join(sandbox, "specs")
	outsideDir := filepath.Join(sandbox, "outside")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(rootDir) error = %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(outsideDir) error = %v", err)
	}

	parentPath := filepath.Join(outsideDir, "base.yaml")
	childPath := filepath.Join(rootDir, "child.yaml")
	if err := osWriteFile(parentPath, "name: outside-base\n"); err != nil {
		t.Fatalf("write parent error = %v", err)
	}
	if err := osWriteFile(childPath, "name: child\nextends: ../outside/base.yaml\n"); err != nil {
		t.Fatalf("write child error = %v", err)
	}

	_, err := ResolveAgentSpec(childPath)
	if err == nil {
		t.Fatal("ResolveAgentSpec() error = nil, want traversal rejection")
	}
	if !strings.Contains(err.Error(), "escapes root spec directory") {
		t.Fatalf("ResolveAgentSpec() error = %v, want contains escapes root spec directory", err)
	}
}

func TestResolveAgentSpecRejectsAbsoluteExtendsPath(t *testing.T) {
	t.Parallel()

	rootDir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(rootDir) error = %v", err)
	}

	parentPath := filepath.Join(rootDir, "base.yaml")
	childPath := filepath.Join(rootDir, "child.yaml")
	if err := osWriteFile(parentPath, "name: base\n"); err != nil {
		t.Fatalf("write parent error = %v", err)
	}
	if err := osWriteFile(childPath, fmt.Sprintf("name: child\nextends: %q\n", parentPath)); err != nil {
		t.Fatalf("write child error = %v", err)
	}

	_, err := ResolveAgentSpec(childPath)
	if err == nil {
		t.Fatal("ResolveAgentSpec() error = nil, want absolute path rejection")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("ResolveAgentSpec() error = %v, want contains must be relative", err)
	}
}

func TestLoadAgentSpecRequiresName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := osWriteFile(path, "model: kimi-k2\n"); err != nil {
		t.Fatalf("write file error = %v", err)
	}
	_, err := LoadAgentSpec(path)
	if err == nil {
		t.Fatal("LoadAgentSpec() error = nil, want name required")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("LoadAgentSpec() error = %v", err)
	}
}

func osWriteFile(path string, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
