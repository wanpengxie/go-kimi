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

func TestParseSkillMarkdownParsesFlowType(t *testing.T) {
	t.Parallel()

	sk, err := parseSkillMarkdown("/tmp/skills/flow", `---
name: flow
type: flow
---
`+"```mermaid\n"+`flowchart TD
BEGIN --> TASK
TASK --> END
`+"```\n")
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if sk.Type != "flow" {
		t.Fatalf("skill.Type = %q, want flow", sk.Type)
	}
	if sk.Flow == nil {
		t.Fatal("skill.Flow = nil, want parsed flow")
	}
}

func TestParseSkillMarkdownFlowParseFailureReturnsError(t *testing.T) {
	t.Parallel()

	_, err := parseSkillMarkdown("/tmp/skills/broken-flow", `---
name: broken-flow
type: flow
---
`+"```mermaid\n"+`flowchart TD
A --> B
`+"```\n")
	if err == nil {
		t.Fatal("parseSkillMarkdown() error = nil, want flow parse failure")
	}
	if !strings.Contains(err.Error(), "parse flow graph") {
		t.Fatalf("parseSkillMarkdown() error = %v, want flow parse context", err)
	}
}

func TestParseSkillMarkdownRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	_, err := parseSkillMarkdown("/tmp/skills/unsupported", `---
name: unsupported
type: custom
---
body
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

// TestParseSkillMarkdownBlockScalarDescription verifies that YAML block
// scalar syntax (`description: |` followed by indented multi-line text) is
// parsed correctly. The pre-yaml parser treated continuation lines as new
// keys and rejected the file outright.
func TestParseSkillMarkdownBlockScalarDescription(t *testing.T) {
	t.Parallel()

	markdown := `---
name: kimi-webbridge
description: |
  Kimi WebBridge lets AI control the user's real browser — navigate, click, type.
  Use this skill whenever the user wants to interact with websites, automate
  browser tasks, or perform any action requiring a real browser.
---
# Kimi WebBridge
body content
`

	sk, err := parseSkillMarkdown("/tmp/skills/kimi-webbridge", markdown)
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if sk.Name != "kimi-webbridge" {
		t.Fatalf("skill.Name = %q, want kimi-webbridge", sk.Name)
	}
	if !strings.Contains(sk.Description, "Kimi WebBridge lets AI control") {
		t.Fatalf("skill.Description = %q, want first sentence", sk.Description)
	}
	if !strings.Contains(sk.Description, "interact with websites") {
		t.Fatalf("skill.Description = %q, want continuation line", sk.Description)
	}
	if !strings.Contains(sk.Content, "# Kimi WebBridge") {
		t.Fatalf("skill.Content = %q, want body intact", sk.Content)
	}
}

// TestParseSkillMarkdownFoldedScalarDescription covers the `>` folded form,
// where line breaks become spaces.
func TestParseSkillMarkdownFoldedScalarDescription(t *testing.T) {
	t.Parallel()

	markdown := `---
name: folded-demo
description: >
  This description spans
  multiple lines but should
  fold into one paragraph.
---
body
`

	sk, err := parseSkillMarkdown("/tmp/skills/folded-demo", markdown)
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if !strings.Contains(sk.Description, "spans multiple lines") {
		t.Fatalf("skill.Description = %q, want folded content", sk.Description)
	}
}

// TestParseSkillMarkdownQuotedStringWithColon ensures that YAML quoted
// scalars survive — the legacy parser would split on the first `:` and
// truncate.
func TestParseSkillMarkdownQuotedStringWithColon(t *testing.T) {
	t.Parallel()

	markdown := `---
name: quoted
description: "Use when X: do Y instead"
---
body
`

	sk, err := parseSkillMarkdown("/tmp/skills/quoted", markdown)
	if err != nil {
		t.Fatalf("parseSkillMarkdown() error = %v", err)
	}
	if sk.Description != "Use when X: do Y instead" {
		t.Fatalf("skill.Description = %q, want full quoted value", sk.Description)
	}
}

// TestParseSkillMarkdownInvalidYAMLReturnsError verifies that genuinely
// malformed YAML (not just unrecognized layout) yields a parse error with
// useful context.
func TestParseSkillMarkdownInvalidYAMLReturnsError(t *testing.T) {
	t.Parallel()

	_, err := parseSkillMarkdown("/tmp/skills/broken", `---
name: broken
description: "unterminated
---
body
`)
	if err == nil {
		t.Fatal("parseSkillMarkdown() error = nil, want yaml parse failure")
	}
	if !strings.Contains(err.Error(), "frontmatter yaml") {
		t.Fatalf("parseSkillMarkdown() error = %v, want yaml context", err)
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
