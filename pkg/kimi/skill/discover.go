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

// ParseError describes a single SKILL.md that failed to parse.
//
// DiscoverSkills and DiscoverFromRoots collect ParseError values for each
// invalid file rather than aborting discovery — callers can surface them as
// warnings while still using the partial result for the files that did parse.
type ParseError struct {
	// Path is the absolute path of the SKILL.md file that failed.
	Path string
	// Err is the underlying error from ParseSkillFile.
	Err error
}

// Error implements the error interface.
func (e ParseError) Error() string {
	return fmt.Sprintf("skill: parse %q: %v", e.Path, e.Err)
}

// Unwrap exposes the underlying cause to errors.Is / errors.As.
func (e ParseError) Unwrap() error {
	return e.Err
}

// DiscoverSkills finds and parses skills under one root directory.
// The directory layout is expected to be:
//
//	<root>/<skill-name>/SKILL.md
//
// A nil error indicates the root itself was reachable. Individual SKILL.md
// parse failures are returned via the ParseError slice and do not stop
// discovery — the returned skill slice contains every file that did parse
// successfully.
func DiscoverSkills(dir string) ([]*Skill, []ParseError, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil, errors.New("skill: discover dir is required")
	}
	dir = filepath.Clean(dir)

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("skill: read dir %q: %w", dir, err)
	}

	skills := make([]*Skill, 0, len(entries))
	var parseErrs []ParseError
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
			return nil, parseErrs, fmt.Errorf("skill: stat %q: %w", skillFile, statErr)
		}
		if info.IsDir() {
			continue
		}

		skill, parseErr := ParseSkillFile(skillFile)
		if parseErr != nil {
			parseErrs = append(parseErrs, ParseError{Path: skillFile, Err: parseErr})
			continue
		}
		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].Dir < skills[j].Dir
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, parseErrs, nil
}

// DiscoverFromRoots discovers skills from multiple roots.
// Later roots have higher priority and overwrite earlier roots with the same skill name.
//
// As with DiscoverSkills, a nil error indicates every root that exists was
// readable. Per-file parse failures from any root are concatenated into the
// returned ParseError slice (in root iteration order) but never short-circuit
// discovery: the caller decides whether to log them as warnings and continue
// or surface them as a fatal condition.
func DiscoverFromRoots(roots []string) (map[string]*Skill, []ParseError, error) {
	merged := make(map[string]*Skill)
	var allParseErrs []ParseError
	for i := range roots {
		root := strings.TrimSpace(roots[i])
		if root == "" {
			continue
		}

		skills, parseErrs, err := DiscoverSkills(root)
		if err != nil {
			return nil, allParseErrs, fmt.Errorf("skill: discover from root %q: %w", root, err)
		}
		if len(parseErrs) > 0 {
			allParseErrs = append(allParseErrs, parseErrs...)
		}
		for j := range skills {
			skill := skills[j]
			if skill == nil || strings.TrimSpace(skill.Name) == "" {
				continue
			}
			merged[normalizeSkillName(skill.Name)] = skill
		}
	}
	return merged, allParseErrs, nil
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
