package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverSkillsFindsSubDirSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkillMarkdown(t, filepath.Join(root, "a-skill", FileName), "alpha", "desc-alpha", "body-alpha")
	writeSkillMarkdown(t, filepath.Join(root, "b-skill", FileName), "beta", "desc-beta", "body-beta")
	if err := os.MkdirAll(filepath.Join(root, "not-a-skill"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	skills, parseErrs, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	if len(parseErrs) != 0 {
		t.Fatalf("DiscoverSkills() parseErrs = %v, want none", parseErrs)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2", len(skills))
	}
	if skills[0].Name != "alpha" || skills[1].Name != "beta" {
		t.Fatalf("skill names = [%q %q], want [alpha beta]", skills[0].Name, skills[1].Name)
	}
	if skills[0].Description != "desc-alpha" {
		t.Fatalf("skills[0].Description = %q, want desc-alpha", skills[0].Description)
	}
}

func TestDiscoverSkillsMissingRootReturnsEmpty(t *testing.T) {
	t.Parallel()

	skills, parseErrs, err := DiscoverSkills(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	if len(parseErrs) != 0 {
		t.Fatalf("DiscoverSkills() parseErrs = %v, want none", parseErrs)
	}
	if len(skills) != 0 {
		t.Fatalf("len(skills) = %d, want 0", len(skills))
	}
}

func TestDiscoverFromRootsHigherPriorityOverrides(t *testing.T) {
	t.Parallel()

	low := t.TempDir()
	high := t.TempDir()

	writeSkillMarkdown(t, filepath.Join(low, "demo", FileName), "demo", "from-low", "low body")
	writeSkillMarkdown(t, filepath.Join(high, "demo", FileName), "demo", "from-high", "high body")
	writeSkillMarkdown(t, filepath.Join(low, "only-low", FileName), "only-low", "low", "low")

	skills, parseErrs, err := DiscoverFromRoots([]string{low, high})
	if err != nil {
		t.Fatalf("DiscoverFromRoots() error = %v", err)
	}
	if len(parseErrs) != 0 {
		t.Fatalf("DiscoverFromRoots() parseErrs = %v, want none", parseErrs)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2", len(skills))
	}

	if got := skills["demo"]; got == nil || got.Description != "from-high" {
		t.Fatalf("skills[demo] = %#v, want description from-high", got)
	}
	if got := skills["only-low"]; got == nil || got.Description != "low" {
		t.Fatalf("skills[only-low] = %#v, want from low root", got)
	}
}

func TestDiscoverFromRootsNormalizesNameCase(t *testing.T) {
	t.Parallel()

	low := t.TempDir()
	high := t.TempDir()

	writeSkillMarkdown(t, filepath.Join(low, "demo", FileName), "Demo", "from-low", "low body")
	writeSkillMarkdown(t, filepath.Join(high, "demo", FileName), "demo", "from-high", "high body")

	skills, parseErrs, err := DiscoverFromRoots([]string{low, high})
	if err != nil {
		t.Fatalf("DiscoverFromRoots() error = %v", err)
	}
	if len(parseErrs) != 0 {
		t.Fatalf("DiscoverFromRoots() parseErrs = %v, want none", parseErrs)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}
	if got := skills["demo"]; got == nil || got.Description != "from-high" {
		t.Fatalf("skills[demo] = %#v, want normalized key demo from-high", got)
	}
}

func TestDefaultSkillRootsOrder(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}

	roots := DefaultSkillRoots(workDir)
	if len(roots) != 3 {
		t.Fatalf("len(roots) = %d, want 3", len(roots))
	}

	wantBuiltin := filepath.Join(workDir, filepath.FromSlash("builtin/skills"))
	wantUser := filepath.Join(home, ".kimi", "skills")
	wantProject := filepath.Join(workDir, filepath.FromSlash(".kimi/skills"))

	if roots[0] != wantBuiltin || roots[1] != wantUser || roots[2] != wantProject {
		t.Fatalf("roots = %#v, want [%q %q %q]", roots, wantBuiltin, wantUser, wantProject)
	}
}

// TestDiscoverSkillsPartialFailureCollectsParseErrors verifies that an
// invalid SKILL.md does not short-circuit discovery: the valid sibling is
// still returned, and the bad file is reported via ParseErrors.
func TestDiscoverSkillsPartialFailureCollectsParseErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkillMarkdown(t, filepath.Join(root, "good", FileName), "good", "good desc", "body")

	badPath := filepath.Join(root, "bad", FileName)
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	// Missing closing `---` marker → parseFrontmatter fails.
	if err := os.WriteFile(badPath, []byte("---\nname: bad\ndescription: oops\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	skills, parseErrs, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "good" {
		t.Fatalf("skills = %#v, want one good skill", skills)
	}
	if len(parseErrs) != 1 {
		t.Fatalf("parseErrs = %v, want one entry", parseErrs)
	}
	if parseErrs[0].Path != badPath {
		t.Fatalf("parseErrs[0].Path = %q, want %q", parseErrs[0].Path, badPath)
	}
	if parseErrs[0].Err == nil {
		t.Fatal("parseErrs[0].Err = nil, want underlying error")
	}
	if !strings.Contains(parseErrs[0].Error(), badPath) {
		t.Fatalf("parseErrs[0].Error() = %q, want path embedded", parseErrs[0].Error())
	}
}

// TestDiscoverFromRootsAggregatesParseErrorsAcrossRoots verifies that parse
// errors from multiple roots are concatenated and that the valid skill from
// the failing root is still merged.
func TestDiscoverFromRootsAggregatesParseErrorsAcrossRoots(t *testing.T) {
	t.Parallel()

	rootA := t.TempDir()
	rootB := t.TempDir()

	writeSkillMarkdown(t, filepath.Join(rootA, "alpha", FileName), "alpha", "alpha desc", "body")
	// Invalid skill in rootA.
	badPathA := filepath.Join(rootA, "broken", FileName)
	if err := os.MkdirAll(filepath.Dir(badPathA), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(badPathA, []byte("not yaml\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	writeSkillMarkdown(t, filepath.Join(rootB, "beta", FileName), "beta", "beta desc", "body")

	skills, parseErrs, err := DiscoverFromRoots([]string{rootA, rootB})
	if err != nil {
		t.Fatalf("DiscoverFromRoots() error = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2 (alpha + beta)", len(skills))
	}
	if skills["alpha"] == nil || skills["beta"] == nil {
		t.Fatalf("skills = %#v, want both alpha and beta", skills)
	}
	if len(parseErrs) != 1 {
		t.Fatalf("parseErrs = %v, want exactly one", parseErrs)
	}
	if parseErrs[0].Path != badPathA {
		t.Fatalf("parseErrs[0].Path = %q, want %q", parseErrs[0].Path, badPathA)
	}
}

func writeSkillMarkdown(t *testing.T, path, name, description, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}

	content := "---\n" +
		"name: " + name + "\n" +
		"description: " + description + "\n" +
		"type: standard\n" +
		"---\n" +
		body + "\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}
