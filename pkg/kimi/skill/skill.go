package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// FileName is the canonical skill definition file name.
	FileName = "SKILL.md"

	standardType = "standard"
)

// Skill is one parsed SKILL.md definition.
type Skill struct {
	Name        string
	Description string
	Type        string
	Dir         string
	Content     string
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

	name := strings.TrimSpace(fields["name"])
	if name == "" {
		return nil, errors.New("skill: frontmatter field name is required")
	}

	skillType := strings.TrimSpace(fields["type"])
	if skillType == "" {
		skillType = standardType
	}
	if skillType != standardType {
		return nil, fmt.Errorf("skill: unsupported type %q", skillType)
	}

	return &Skill{
		Name:        name,
		Description: strings.TrimSpace(fields["description"]),
		Type:        skillType,
		Dir:         filepath.Clean(strings.TrimSpace(dir)),
		Content:     body,
	}, nil
}

func parseFrontmatter(markdown string) (map[string]string, string, error) {
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

	fields := make(map[string]string, 3)
	for i := 1; i < closeIdx; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("skill: invalid frontmatter line %q", line)
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "name", "description", "type":
			fields[key] = value
		}
	}

	body := ""
	if closeIdx+1 < len(lines) {
		body = strings.Join(lines[closeIdx+1:], "\n")
	}

	return fields, body, nil
}

func normalizeMarkdown(markdown string) string {
	markdown = strings.TrimPrefix(markdown, "\ufeff")
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	return strings.ReplaceAll(markdown, "\r", "\n")
}
