package skill

import (
	"os"
	"path/filepath"
	"runtime"
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

	skills, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
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

	skills, err := DiscoverSkills(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
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

	skills, err := DiscoverFromRoots([]string{low, high})
	if err != nil {
		t.Fatalf("DiscoverFromRoots() error = %v", err)
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

	skills, err := DiscoverFromRoots([]string{low, high})
	if err != nil {
		t.Fatalf("DiscoverFromRoots() error = %v", err)
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
