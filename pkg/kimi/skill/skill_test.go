package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillMarkdownSuccess(t *testing.T) {
	t.Parallel()

	sk, err := parseSkillMarkdown("/tmp/skills/demo", `---
name: demo
description: demo description
type: standard
---
step one
step two
`)
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if sk.Name != "demo" {
		t.Fatalf("skill.Name = %q, want demo", sk.Name)
	}
	if sk.Description != "demo description" {
		t.Fatalf("skill.Description = %q, want demo description", sk.Description)
	}
	if sk.Type != "standard" {
		t.Fatalf("skill.Type = %q, want standard", sk.Type)
	}
	if got, want := filepath.Clean(sk.Dir), filepath.Clean("/tmp/skills/demo"); got != want {
		t.Fatalf("skill.Dir = %q, want %q", got, want)
	}
	if !strings.Contains(sk.Content, "step one") || !strings.Contains(sk.Content, "step two") {
		t.Fatalf("skill.Content = %q, want contains body lines", sk.Content)
	}
}

func TestParseSkillMarkdownDefaultsTypeToStandard(t *testing.T) {
	t.Parallel()

	sk, err := parseSkillMarkdown("/tmp/skills/no-type", `---
name: no-type
description: demo
---
hello
`)
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if sk.Type != "standard" {
		t.Fatalf("skill.Type = %q, want standard", sk.Type)
	}
}

func TestParseSkillMarkdownRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	_, err := parseSkillMarkdown("/tmp/skills/flow", `---
name: flow
type: flow
---
graph TD
`)
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("parseSkillMarkdown() error = %v, want unsupported type", err)
	}
}

func TestParseSkillMarkdownRequiresName(t *testing.T) {
	t.Parallel()

	_, err := parseSkillMarkdown("/tmp/skills/noname", `---
description: missing name
type: standard
---
body
`)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("parseSkillMarkdown() error = %v, want name required", err)
	}
}

func TestParseFrontmatterRequiresMarkers(t *testing.T) {
	t.Parallel()

	_, _, err := parseFrontmatter("name: demo\nbody")
	if err == nil || !strings.Contains(err.Error(), "opening marker") {
		t.Fatalf("parseFrontmatter() error = %v, want opening marker error", err)
	}
}

func TestParseSkillFileReadsMarkdown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, FileName)
	markdown := "\ufeff---\r\nname: from-file\r\ndescription: from disk\r\ntype: standard\r\n---\r\nhello\r\n"
	if err := os.WriteFile(filePath, []byte(markdown), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	sk, err := ParseSkillFile(filePath)
	if err != nil {
		t.Fatalf("ParseSkillFile() error = %v", err)
	}
	if sk.Name != "from-file" {
		t.Fatalf("skill.Name = %q, want from-file", sk.Name)
	}
	if sk.Description != "from disk" {
		t.Fatalf("skill.Description = %q, want from disk", sk.Description)
	}
	if sk.Type != "standard" {
		t.Fatalf("skill.Type = %q, want standard", sk.Type)
	}
	if got, want := filepath.Clean(sk.Dir), filepath.Clean(root); got != want {
		t.Fatalf("skill.Dir = %q, want %q", got, want)
	}
	if strings.TrimSpace(sk.Content) != "hello" {
		t.Fatalf("skill.Content = %q, want hello", sk.Content)
	}
}
