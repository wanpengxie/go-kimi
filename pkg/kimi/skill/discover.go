package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	projectSkillDir = ".kimi/skills"
	builtinSkillDir = "builtin/skills"
)

// DiscoverSkills finds and parses skills under one root directory.
// The directory layout is expected to be:
//
//	<root>/<skill-name>/SKILL.md
func DiscoverSkills(dir string) ([]*Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("skill: discover dir is required")
	}
	dir = filepath.Clean(dir)

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill: read dir %q: %w", dir, err)
	}

	skills := make([]*Skill, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}

		skillFile := filepath.Join(dir, entry.Name(), FileName)
		info, statErr := os.Stat(skillFile)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("skill: stat %q: %w", skillFile, statErr)
		}
		if info.IsDir() {
			continue
		}

		skill, parseErr := ParseSkillFile(skillFile)
		if parseErr != nil {
			return nil, parseErr
		}
		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].Dir < skills[j].Dir
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// DiscoverFromRoots discovers skills from multiple roots.
// Later roots have higher priority and overwrite earlier roots with the same skill name.
func DiscoverFromRoots(roots []string) (map[string]*Skill, error) {
	merged := make(map[string]*Skill)
	for i := range roots {
		root := strings.TrimSpace(roots[i])
		if root == "" {
			continue
		}

		skills, err := DiscoverSkills(root)
		if err != nil {
			return nil, fmt.Errorf("skill: discover from root %q: %w", root, err)
		}
		for j := range skills {
			skill := skills[j]
			if skill == nil || strings.TrimSpace(skill.Name) == "" {
				continue
			}
			merged[normalizeSkillName(skill.Name)] = skill
		}
	}
	return merged, nil
}

// DefaultSkillRoots returns built-in/user/project skill roots in ascending priority:
// built-in -> user -> project.
func DefaultSkillRoots(workDir string) []string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workDir = cwd
		}
	}

	roots := make([]string, 0, 3)
	if workDir == "" {
		roots = append(roots, filepath.Clean(builtinSkillDir))
	} else {
		roots = append(roots, filepath.Join(workDir, filepath.FromSlash(builtinSkillDir)))
	}

	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".kimi", "skills"))
	}

	if workDir == "" {
		roots = append(roots, filepath.Clean(projectSkillDir))
	} else {
		roots = append(roots, filepath.Join(workDir, filepath.FromSlash(projectSkillDir)))
	}
	return roots
}

func normalizeSkillName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
