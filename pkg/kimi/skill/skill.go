package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skillflow "github.com/wanpengxie/go-kimi/pkg/kimi/skill/flow"
	"gopkg.in/yaml.v3"
)

const (
	// FileName is the canonical skill definition file name.
	FileName = "SKILL.md"

	standardType = "standard"
	flowType     = "flow"
)

// Skill is one parsed SKILL.md definition.
type Skill struct {
	Name        string
	Description string
	Type        string
	Dir         string
	Content     string
	Flow        *skillflow.Flow
}

// ParseSkillFile loads and parses one SKILL.md file.
func ParseSkillFile(path string) (*Skill, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("skill: file path is required")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read %q: %w", path, err)
	}

	skill, err := parseSkillMarkdown(filepath.Dir(path), string(content))
	if err != nil {
		return nil, fmt.Errorf("skill: parse %q: %w", path, err)
	}
	return skill, nil
}

func parseSkillMarkdown(dir, markdown string) (*Skill, error) {
	fields, body, err := parseFrontmatter(markdown)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(stringField(fields, "name"))
	if name == "" {
		return nil, errors.New("skill: frontmatter field name is required")
	}

	skillType := strings.ToLower(strings.TrimSpace(stringField(fields, "type")))
	if skillType == "" {
		skillType = standardType
	}
	if skillType != standardType && skillType != flowType {
		return nil, fmt.Errorf("skill: unsupported type %q", skillType)
	}

	var parsedFlow *skillflow.Flow
	if skillType == flowType {
		flowGraph, flowErr := parseFlowFromSkill(markdown)
		if flowErr != nil {
			return nil, fmt.Errorf("skill: parse flow graph: %w", flowErr)
		}
		parsedFlow = flowGraph
	}

	return &Skill{
		Name:        name,
		Description: strings.TrimSpace(stringField(fields, "description")),
		Type:        skillType,
		Dir:         filepath.Clean(strings.TrimSpace(dir)),
		Content:     body,
		Flow:        parsedFlow,
	}, nil
}

// parseFrontmatter extracts the YAML block delimited by `---` markers at the
// top of a SKILL.md file and returns the parsed key/value map plus the
// remaining markdown body.
//
// The YAML block is parsed by gopkg.in/yaml.v3, so all standard YAML features
// are supported, including block scalars (`description: |` followed by
// indented lines), quoted strings, lists, and nested maps.
func parseFrontmatter(markdown string) (map[string]any, string, error) {
	normalized := normalizeMarkdown(markdown)
	lines := strings.Split(normalized, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", errors.New("skill: missing frontmatter opening marker")
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, "", errors.New("skill: missing frontmatter closing marker")
	}

	yamlBlock := strings.Join(lines[1:closeIdx], "\n")
	raw := make(map[string]any)
	if strings.TrimSpace(yamlBlock) != "" {
		if err := yaml.Unmarshal([]byte(yamlBlock), &raw); err != nil {
			return nil, "", fmt.Errorf("skill: parse frontmatter yaml: %w", err)
		}
	}

	fields := make(map[string]any, len(raw))
	for key, value := range raw {
		fields[strings.ToLower(strings.TrimSpace(key))] = value
	}

	body := ""
	if closeIdx+1 < len(lines) {
		body = strings.Join(lines[closeIdx+1:], "\n")
	}

	return fields, body, nil
}

// stringField coerces a value extracted from the frontmatter map into a
// string. YAML scalars (string / int / float / bool) are stringified; any
// other shape (sequence, mapping, nil) yields the empty string so callers can
// rely on the standard `required` checks.
func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	v, ok := fields[key]
	if !ok || v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case float64:
		return fmt.Sprintf("%g", typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	default:
		return ""
	}
}

func normalizeMarkdown(markdown string) string {
	markdown = strings.TrimPrefix(markdown, "\ufeff")
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	return strings.ReplaceAll(markdown, "\r", "\n")
}

func parseFlowFromSkill(markdown string) (*skillflow.Flow, error) {
	blocks := iterFencedCodeBlocks(markdown)
	for i := range blocks {
		switch blocks[i].lang {
		case "mermaid":
			return skillflow.ParseMermaidFlowchart(blocks[i].code)
		case "d2":
			return skillflow.ParseD2Flowchart(blocks[i].code)
		}
	}
	return nil, errors.New("flow skills require a mermaid or d2 code block in SKILL.md")
}

type fencedCodeBlock struct {
	lang string
	code string
}

func iterFencedCodeBlocks(markdown string) []fencedCodeBlock {
	lines := strings.Split(normalizeMarkdown(markdown), "\n")
	blocks := make([]fencedCodeBlock, 0)

	inBlock := false
	fenceChar := byte(0)
	fenceLen := 0
	lang := ""
	content := make([]string, 0)

	for i := range lines {
		stripped := strings.TrimLeft(lines[i], " \t")
		if !inBlock {
			opened, openChar, openLen, info := parseFenceOpen(stripped)
			if !opened {
				continue
			}
			inBlock = true
			fenceChar = openChar
			fenceLen = openLen
			lang = normalizeCodeBlockLang(info)
			content = content[:0]
			continue
		}

		if isFenceClose(stripped, fenceChar, fenceLen) {
			blocks = append(blocks, fencedCodeBlock{
				lang: lang,
				code: strings.Trim(strings.Join(content, "\n"), "\n"),
			})
			inBlock = false
			fenceChar = 0
			fenceLen = 0
			lang = ""
			content = content[:0]
			continue
		}

		content = append(content, lines[i])
	}

	return blocks
}

func parseFenceOpen(line string) (bool, byte, int, string) {
	if len(line) < 3 {
		return false, 0, 0, ""
	}
	if line[0] != '`' && line[0] != '~' {
		return false, 0, 0, ""
	}

	fenceChar := line[0]
	count := 0
	for count < len(line) && line[count] == fenceChar {
		count++
	}
	if count < 3 {
		return false, 0, 0, ""
	}
	return true, fenceChar, count, strings.TrimSpace(line[count:])
}

func isFenceClose(line string, fenceChar byte, fenceLen int) bool {
	if fenceChar == 0 || fenceLen < 3 || len(line) < fenceLen || line[0] != fenceChar {
		return false
	}
	count := 0
	for count < len(line) && line[count] == fenceChar {
		count++
	}
	if count < fenceLen {
		return false
	}
	return strings.TrimSpace(line[count:]) == ""
}

func normalizeCodeBlockLang(info string) string {
	info = strings.TrimSpace(info)
	if info == "" {
		return ""
	}

	parts := strings.Fields(info)
	if len(parts) == 0 {
		return ""
	}
	lang := strings.ToLower(strings.TrimSpace(parts[0]))
	if strings.HasPrefix(lang, "{") && strings.HasSuffix(lang, "}") && len(lang) >= 2 {
		lang = strings.TrimSpace(lang[1 : len(lang)-1])
	}
	return lang
}
