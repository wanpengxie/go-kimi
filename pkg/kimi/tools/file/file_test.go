package file

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO5N2ioAAAAASUVORK5CYII="

func TestReadFileExecuteSuccessWithLineRange(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewReadFile(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":        "notes.txt",
		"line_offset": 2,
		"n_lines":     2,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "beta\ngamma" {
		t.Fatalf("result output = %q, want %q", got, "beta\\ngamma")
	}
}

func TestReadFileExecuteMissingFileReturnsErrorResult(t *testing.T) {
	t.Parallel()

	tool := NewReadFile(t.TempDir())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "missing.txt",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "read file") {
		t.Fatalf("result output = %q, want contains read file", resultOutputText(t, result))
	}
}

func TestReadFileExecuteTruncatesLongLine(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "long.txt")
	longLine := strings.Repeat("x", tools.MaxLineLengthChars+100)
	if err := os.WriteFile(path, []byte(longLine), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewReadFile(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "long.txt",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := resultOutputText(t, result)
	if !strings.Contains(output, lineTruncateSuffix) {
		t.Fatalf("result output = %q, want line truncation suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxLineLengthChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxLineLengthChars)
	}
}

func TestReadFileExecuteTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "huge.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefghijklmnop\n", 4000)), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewReadFile(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "huge.txt",
		"n_lines": 4000,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, outputTruncateSuffix) {
		t.Fatalf("result output = %q, want truncated suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxOutputChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxOutputChars)
	}
}

func TestReadFileExecuteLineParamsValidation(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewReadFile(workDir)

	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":        "notes.txt",
		"line_offset": -1,
	})); err == nil {
		t.Fatal("Execute(line_offset=-1) error = nil, want validation error")
	}

	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "notes.txt",
		"n_lines": -1,
	})); err == nil {
		t.Fatal("Execute(n_lines=-1) error = nil, want validation error")
	}

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":        "notes.txt",
		"line_offset": 0,
		"n_lines":     1,
	}))
	if err != nil {
		t.Fatalf("Execute(line_offset=0) error = %v", err)
	}
	if got := resultOutputText(t, result); got != "alpha" {
		t.Fatalf("result output(line_offset=0) = %q, want %q", got, "alpha")
	}
}

func TestWriteFileExecuteWritesAndAppends(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := NewWriteFile(workDir, nil)

	first, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "dir/out.txt",
		"content": "hello",
	}))
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if first.IsError {
		t.Fatalf("first result.IsError = %v, want false", first.IsError)
	}

	second, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "dir/out.txt",
		"content": " world",
		"append":  true,
	}))
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if second.IsError {
		t.Fatalf("second result.IsError = %v, want false", second.IsError)
	}

	content, readErr := os.ReadFile(filepath.Join(workDir, "dir/out.txt"))
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if string(content) != "hello world" {
		t.Fatalf("file content = %q, want %q", string(content), "hello world")
	}
}

func TestWriteFileExecuteApprovalRejected(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	var calledAction string
	var calledDesc string
	tool := NewWriteFile(workDir, func(_ context.Context, action, desc string) (bool, string) {
		calledAction = action
		calledDesc = desc
		return false, "blocked by policy"
	})

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "blocked.txt",
		"content": "data",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if calledAction != writeToolName {
		t.Fatalf("approver action = %q, want %q", calledAction, writeToolName)
	}
	if !strings.Contains(calledDesc, "blocked.txt") {
		t.Fatalf("approver desc = %q, want contains blocked.txt", calledDesc)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected blocked.txt absent, stat err = %v", statErr)
	}
}

func TestWriteFileExecuteOverwritesExistingFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	target := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(notes.txt) error = %v", err)
	}

	tool := NewWriteFile(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "notes.txt",
		"content": "after",
	}))
	if err != nil {
		t.Fatalf("Execute(overwrite) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("os.ReadFile(notes.txt) error = %v", readErr)
	}
	if got := string(content); got != "after" {
		t.Fatalf("file content = %q, want %q", got, "after")
	}
}

func TestWriteFileExecuteSupportsUnicodeAndMultilineContent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := NewWriteFile(workDir, nil)

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "unicode.txt",
		"content": "你好，kimi\n第二行",
	}))
	if err != nil {
		t.Fatalf("Execute(unicode multiline) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(filepath.Join(workDir, "unicode.txt"))
	if readErr != nil {
		t.Fatalf("os.ReadFile(unicode.txt) error = %v", readErr)
	}
	if got := string(content); got != "你好，kimi\n第二行" {
		t.Fatalf("file content = %q, want unicode multiline content", got)
	}
}

func TestWriteFileExecuteAllowsEmptyContent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := NewWriteFile(workDir, nil)

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "empty.txt",
		"content": "",
	}))
	if err != nil {
		t.Fatalf("Execute(empty content) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	info, statErr := os.Stat(filepath.Join(workDir, "empty.txt"))
	if statErr != nil {
		t.Fatalf("os.Stat(empty.txt) error = %v", statErr)
	}
	if info.Size() != 0 {
		t.Fatalf("empty.txt size = %d, want 0", info.Size())
	}
}

func TestWriteFileExecuteRejectsPathOutsideWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")

	tool := NewWriteFile(workDir, nil)
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    outsidePath,
		"content": "blocked",
	})); err == nil {
		t.Fatal("Execute(outside workdir) error = nil, want escape rejection")
	}
}

func TestWriteFileExecuteAppendCreatesMissingFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := NewWriteFile(workDir, nil)

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "new.log",
		"content": "entry-1",
		"append":  true,
	}))
	if err != nil {
		t.Fatalf("Execute(append missing) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(filepath.Join(workDir, "new.log"))
	if readErr != nil {
		t.Fatalf("os.ReadFile(new.log) error = %v", readErr)
	}
	if got := string(content); got != "entry-1" {
		t.Fatalf("file content = %q, want %q", got, "entry-1")
	}
}

func TestWriteFileExecuteLargeContent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	tool := NewWriteFile(workDir, nil)
	large := strings.Repeat("0123456789", 20_000)

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":    "large.txt",
		"content": large,
	}))
	if err != nil {
		t.Fatalf("Execute(large content) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(filepath.Join(workDir, "large.txt"))
	if readErr != nil {
		t.Fatalf("os.ReadFile(large.txt) error = %v", readErr)
	}
	if len(content) != len(large) {
		t.Fatalf("len(file content) = %d, want %d", len(content), len(large))
	}
}

func TestResolvePathRejectsRelativeEscape(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	_, err := resolvePath(workDir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("resolvePath() error = nil, want escape rejection")
	}
}

func TestResolvePathRejectsAbsoluteEscape(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")

	_, err := resolvePath(workDir, outsidePath)
	if err == nil {
		t.Fatal("resolvePath() error = nil, want absolute escape rejection")
	}
}

func TestResolvePathRejectsSymlinkFileEscape(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink tests require elevated privileges on windows")
	}

	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	linkPath := filepath.Join(workDir, "escape.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}

	_, err := resolvePath(workDir, "escape.txt")
	if err == nil {
		t.Fatal("resolvePath() error = nil, want symlink escape rejection")
	}
}

func TestResolvePathRejectsSymlinkParentEscape(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink tests require elevated privileges on windows")
	}

	workDir := t.TempDir()
	outsideDir := t.TempDir()
	parentLink := filepath.Join(workDir, "linked-dir")
	if err := os.Symlink(outsideDir, parentLink); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}

	_, err := resolvePath(workDir, filepath.Join("linked-dir", "new.txt"))
	if err == nil {
		t.Fatal("resolvePath() error = nil, want symlink parent escape rejection")
	}
}

func TestResolvePathAllowsPathInsideWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	got, err := resolvePath(workDir, "sub/ok.txt")
	if err != nil {
		t.Fatalf("resolvePath() error = %v", err)
	}

	want := filepath.Join(workDir, "sub", "ok.txt")
	if got != filepath.Clean(want) {
		t.Fatalf("resolvePath() = %q, want %q", got, filepath.Clean(want))
	}
}

func TestResolvePathAllowsExistingFileInsideWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "sub"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "sub", "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	got, err := resolvePath(workDir, "sub/ok.txt")
	if err != nil {
		t.Fatalf("resolvePath() error = %v", err)
	}

	want := filepath.Join(workDir, "sub", "ok.txt")
	if got != filepath.Clean(want) {
		t.Fatalf("resolvePath() = %q, want %q", got, filepath.Clean(want))
	}
}

func TestGlobExecuteRespectsLimitAndRelativePaths(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.log"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", name, err)
		}
	}

	tool := NewGlob(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "*.txt",
		"limit":   1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "a.txt" {
		t.Fatalf("result output = %q, want %q", got, "a.txt")
	}
}

func TestGlobExecuteInvalidPatternReturnsError(t *testing.T) {
	t.Parallel()

	tool := NewGlob(t.TempDir())
	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "[",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid pattern error")
	}
}

func TestGlobExecuteRejectsEscapedPattern(t *testing.T) {
	t.Parallel()

	tool := NewGlob(t.TempDir())
	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "../../**",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want escape rejection error")
	}
}

func TestGlobExecuteNoMatchesReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(a.txt) error = %v", err)
	}

	tool := NewGlob(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "*.md",
	}))
	if err != nil {
		t.Fatalf("Execute(no matches) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "" {
		t.Fatalf("result output = %q, want empty string", got)
	}
}

func TestGlobExecuteSupportsSingleCharacterWildcard(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, name := range []string{"a1.txt", "a2.txt", "a10.txt"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", name, err)
		}
	}

	tool := NewGlob(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "a?.txt",
	}))
	if err != nil {
		t.Fatalf("Execute(?) error = %v", err)
	}
	if got := resultOutputText(t, result); got != "a1.txt\na2.txt" {
		t.Fatalf("result output = %q, want %q", got, "a1.txt\\na2.txt")
	}
}

func TestGlobExecuteSupportsCharacterClassPattern(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, name := range []string{"file1.log", "file2.log", "file3.log"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", name, err)
		}
	}

	tool := NewGlob(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "file[1-2].log",
	}))
	if err != nil {
		t.Fatalf("Execute([1-2]) error = %v", err)
	}
	if got := resultOutputText(t, result); got != "file1.log\nfile2.log" {
		t.Fatalf("result output = %q, want %q", got, "file1.log\\nfile2.log")
	}
}

func TestGlobExecuteComplexPatternAndLimit(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, name := range []string{
		filepath.Join("pkg", "alpha_test.go"),
		filepath.Join("pkg", "beta_test.go"),
		filepath.Join("pkg", "gamma.go"),
	} {
		full := filepath.Join(workDir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", full, err)
		}
	}

	tool := NewGlob(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "pkg/*_test.go",
		"limit":   2,
	}))
	if err != nil {
		t.Fatalf("Execute(complex pattern) error = %v", err)
	}
	if got := resultOutputText(t, result); got != "pkg/alpha_test.go\npkg/beta_test.go" {
		t.Fatalf("result output = %q, want sorted two test files", got)
	}
}

func TestGlobExecuteNonexistentDirectoryReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	tool := NewGlob(t.TempDir())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "missing/*.txt",
	}))
	if err != nil {
		t.Fatalf("Execute(nonexistent dir) error = %v", err)
	}
	if got := resultOutputText(t, result); got != "" {
		t.Fatalf("result output = %q, want empty", got)
	}
}

func TestGlobExecuteRejectsAbsolutePathOutsideWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outsideDir := t.TempDir()

	tool := NewGlob(workDir)
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": filepath.Join(outsideDir, "*.txt"),
	})); err == nil {
		t.Fatal("Execute(outside absolute pattern) error = nil, want escape rejection")
	}
}

func TestGrepExecuteRecursiveWithContext(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(a.txt) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "sub"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "sub", "b.txt"), []byte("alpha\nbeta two\ngamma\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sub/b.txt) error = %v", err)
	}

	tool := NewGrep(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern":       "two",
		"path":          ".",
		"recursive":     true,
		"context_lines": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	output := resultOutputText(t, result)
	for _, want := range []string{
		"a.txt:1: one",
		"a.txt:2: two",
		"a.txt:3: three",
		"sub/b.txt:1: alpha",
		"sub/b.txt:2: beta two",
		"sub/b.txt:3: gamma",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("result output = %q, want contains %q", output, want)
		}
	}
}

func TestGrepExecutePathMissingReturnsErrorResult(t *testing.T) {
	t.Parallel()

	tool := NewGrep(t.TempDir())
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "x",
		"path":    "missing",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "grep path") {
		t.Fatalf("result output = %q, want contains grep path", resultOutputText(t, result))
	}
}

func TestGrepExecuteInvalidRegexReturnsError(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewGrep(workDir)
	_, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "[",
		"path":    "a.txt",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil, want regex compile error")
	}
}

func TestGrepExecuteNoMatchesReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "single.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(single.txt) error = %v", err)
	}

	tool := NewGrep(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "missing-pattern",
		"path":    "single.txt",
	}))
	if err != nil {
		t.Fatalf("Execute(no matches) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "" {
		t.Fatalf("result output = %q, want empty", got)
	}
}

func TestGrepExecuteSingleFileIncludesLineNumbers(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("one\ntwo target\nthree\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(notes.txt) error = %v", err)
	}

	tool := NewGrep(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "target",
		"path":    "notes.txt",
	}))
	if err != nil {
		t.Fatalf("Execute(single file) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}
	if got := resultOutputText(t, result); got != "notes.txt:2: two target" {
		t.Fatalf("result output = %q, want %q", got, "notes.txt:2: two target")
	}
}

func TestGrepExecuteTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	lines := make([]string, 0, 6000)
	for i := 0; i < 6000; i++ {
		lines = append(lines, "target-line-"+strings.Repeat("x", 20))
	}
	if err := os.WriteFile(filepath.Join(workDir, "big.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("os.WriteFile(big.txt) error = %v", err)
	}

	tool := NewGrep(workDir)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"pattern": "target-line",
		"path":    "big.txt",
	}))
	if err != nil {
		t.Fatalf("Execute(long output) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	output := resultOutputText(t, result)
	if !strings.Contains(output, outputTruncateSuffix) {
		t.Fatalf("result output = %q, want truncation suffix", output)
	}
	if utf8.RuneCountInString(output) > tools.MaxOutputChars {
		t.Fatalf("output rune count = %d, want <= %d", utf8.RuneCountInString(output), tools.MaxOutputChars)
	}
}

func TestStrReplaceExecuteSuccess(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "doc.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "doc.txt",
		"old_string": "world",
		"new_string": "gopher",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if string(content) != "hello gopher" {
		t.Fatalf("file content = %q, want %q", string(content), "hello gopher")
	}
}

func TestStrReplaceExecuteUniquenessChecks(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "doc.txt"), []byte("hello world"), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		tool := NewStrReplace(workDir, nil)
		result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
			"path":       "doc.txt",
			"old_string": "absent",
			"new_string": "x",
		}))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.IsError {
			t.Fatalf("result.IsError = %v, want true", result.IsError)
		}
	})

	t.Run("multiple matches", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "doc.txt"), []byte("x y x"), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		tool := NewStrReplace(workDir, nil)
		result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
			"path":       "doc.txt",
			"old_string": "x",
			"new_string": "z",
		}))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.IsError {
			t.Fatalf("result.IsError = %v, want true", result.IsError)
		}
		if !strings.Contains(resultOutputText(t, result), "not unique") {
			t.Fatalf("result output = %q, want contains not unique", resultOutputText(t, result))
		}
	})
}

func TestStrReplaceExecuteApprovalRejected(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "doc.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	var calledAction string
	tool := NewStrReplace(workDir, func(_ context.Context, action, _ string) (bool, string) {
		calledAction = action
		return false, "blocked"
	})

	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "doc.txt",
		"old_string": "world",
		"new_string": "gopher",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if calledAction != strReplaceToolName {
		t.Fatalf("approver action = %q, want %q", calledAction, strReplaceToolName)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if string(content) != "hello world" {
		t.Fatalf("file content = %q, want unchanged %q", string(content), "hello world")
	}
}

func TestStrReplaceExecuteMultilineContent(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "doc.txt")
	original := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile(doc.txt) error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "doc.txt",
		"old_string": "line2\nline3",
		"new_string": "middle\nend",
	}))
	if err != nil {
		t.Fatalf("Execute(multiline) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile(doc.txt) error = %v", readErr)
	}
	if got := string(content); got != "line1\nmiddle\nend\n" {
		t.Fatalf("file content = %q, want multiline replaced content", got)
	}
}

func TestStrReplaceExecuteUnicodeReplacement(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "unicode.txt")
	if err := os.WriteFile(path, []byte("你好 世界"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(unicode.txt) error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "unicode.txt",
		"old_string": "世界",
		"new_string": "Kimi",
	}))
	if err != nil {
		t.Fatalf("Execute(unicode) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile(unicode.txt) error = %v", readErr)
	}
	if got := string(content); got != "你好 Kimi" {
		t.Fatalf("file content = %q, want %q", got, "你好 Kimi")
	}
}

func TestStrReplaceExecuteRejectsPathOutsideWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(outside.txt) error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	if _, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       target,
		"old_string": "hello",
		"new_string": "world",
	})); err == nil {
		t.Fatal("Execute(outside workdir) error = nil, want escape rejection")
	}
}

func TestStrReplaceExecuteMissingFileReturnsErrorResult(t *testing.T) {
	t.Parallel()

	tool := NewStrReplace(t.TempDir(), nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "missing.txt",
		"old_string": "a",
		"new_string": "b",
	}))
	if err != nil {
		t.Fatalf("Execute(missing file) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "read file") {
		t.Fatalf("result output = %q, want contains read file", resultOutputText(t, result))
	}
}

func TestStrReplaceExecuteDirectoryPathReturnsErrorResult(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "dir"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(dir) error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "dir",
		"old_string": "a",
		"new_string": "b",
	}))
	if err != nil {
		t.Fatalf("Execute(directory path) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
}

func TestStrReplaceExecuteAllowsEmptyReplacement(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	path := filepath.Join(workDir, "doc.txt")
	if err := os.WriteFile(path, []byte("abcXYZdef"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(doc.txt) error = %v", err)
	}

	tool := NewStrReplace(workDir, nil)
	result, err := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path":       "doc.txt",
		"old_string": "XYZ",
		"new_string": "",
	}))
	if err != nil {
		t.Fatalf("Execute(empty replacement) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile(doc.txt) error = %v", readErr)
	}
	if got := string(content); got != "abcdef" {
		t.Fatalf("file content = %q, want %q", got, "abcdef")
	}
}

func TestReadMediaFileExecuteImageSuccess(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	imageBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("DecodeString(tinyPNGBase64) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "tiny.png"), imageBytes, 0o644); err != nil {
		t.Fatalf("os.WriteFile(tiny.png) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, true, false)
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "tiny.png",
	}))
	if execErr != nil {
		t.Fatalf("Execute() error = %v", execErr)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	payload := resultPayloadMap(t, result)
	if got, _ := payload["media_type"].(string); got != "image/png" {
		t.Fatalf("payload.media_type = %q, want %q", got, "image/png")
	}

	parts, ok := payload["content_parts"].(types.ContentParts)
	if !ok {
		t.Fatalf("payload.content_parts type = %T, want types.ContentParts", payload["content_parts"])
	}
	if len(parts) != 1 {
		t.Fatalf("len(content_parts) = %d, want 1", len(parts))
	}
	imagePart, ok := parts[0].(types.ImageURLPart)
	if !ok {
		t.Fatalf("content_parts[0] type = %T, want types.ImageURLPart", parts[0])
	}
	if !strings.HasPrefix(imagePart.ImageURL, "data:image/png;base64,") {
		t.Fatalf("image data url prefix = %q, want data:image/png;base64,...", imagePart.ImageURL)
	}
}

func TestReadMediaFileExecuteSkipsWithoutCapability(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	imageBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("DecodeString(tinyPNGBase64) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "tiny.png"), imageBytes, 0o644); err != nil {
		t.Fatalf("os.WriteFile(tiny.png) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, false, false)
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "tiny.png",
	}))
	if execErr != nil {
		t.Fatalf("Execute() error = %v", execErr)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	payload := resultPayloadMap(t, result)
	if skipped, _ := payload["skipped"].(bool); !skipped {
		t.Fatalf("payload.skipped = %#v, want true", payload["skipped"])
	}
	if reason, _ := payload["reason"].(string); !strings.Contains(reason, "vision") {
		t.Fatalf("payload.reason = %q, want contains vision", reason)
	}
}

func TestReadMediaFileExecuteRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	target := filepath.Join(workDir, "large.bin")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 64)), 0o644); err != nil {
		t.Fatalf("os.WriteFile(large.bin) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, true, true)
	tool.MaxBytes = 16
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "large.bin",
	}))
	if execErr != nil {
		t.Fatalf("Execute() error = %v", execErr)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "larger than") {
		t.Fatalf("result output = %q, want contains larger than", resultOutputText(t, result))
	}
}

func TestReadMediaFileExecuteRejectsNonRegularFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mediaDir := filepath.Join(workDir, "media-dir")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(media-dir) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, true, true)
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "media-dir",
	}))
	if execErr != nil {
		t.Fatalf("Execute() error = %v", execErr)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "not a regular file") {
		t.Fatalf("result output = %q, want contains not a regular file", resultOutputText(t, result))
	}
}

func TestReadMediaFileExecuteRejectsUnexpectedField(t *testing.T) {
	t.Parallel()

	tool := NewReadMediaFile(t.TempDir(), true, true)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"tiny.png","unexpected":true}`)); err == nil {
		t.Fatal("Execute(unexpected field) error = nil, want validation error")
	}
}

func TestReadMediaFileExecuteVideoSuccess(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	videoPath := filepath.Join(workDir, "clip.mp4")
	// Use binary bytes so DetectContentType falls back to extension-based video/mp4.
	videoBytes := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	if err := os.WriteFile(videoPath, videoBytes, 0o644); err != nil {
		t.Fatalf("os.WriteFile(clip.mp4) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, true, true)
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "clip.mp4",
	}))
	if execErr != nil {
		t.Fatalf("Execute(video) error = %v", execErr)
	}
	if result.IsError {
		t.Fatalf("result.IsError = %v, want false", result.IsError)
	}

	payload := resultPayloadMap(t, result)
	if got, _ := payload["media_type"].(string); got != "video/mp4" {
		t.Fatalf("payload.media_type = %q, want %q", got, "video/mp4")
	}

	parts, ok := payload["content_parts"].(types.ContentParts)
	if !ok {
		t.Fatalf("payload.content_parts type = %T, want types.ContentParts", payload["content_parts"])
	}
	if len(parts) != 1 {
		t.Fatalf("len(content_parts) = %d, want 1", len(parts))
	}
	videoPart, ok := parts[0].(types.VideoURLPart)
	if !ok {
		t.Fatalf("content_parts[0] type = %T, want types.VideoURLPart", parts[0])
	}
	if !strings.HasPrefix(videoPart.VideoURL, "data:video/mp4;base64,") {
		t.Fatalf("video data url prefix = %q, want data:video/mp4;base64,...", videoPart.VideoURL)
	}
}

func TestReadMediaFileExecuteRejectsTextFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(note.txt) error = %v", err)
	}

	tool := NewReadMediaFile(workDir, true, true)
	result, execErr := tool.Execute(context.Background(), mustParams(t, map[string]any{
		"path": "note.txt",
	}))
	if execErr != nil {
		t.Fatalf("Execute(text file) error = %v", execErr)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = %v, want true", result.IsError)
	}
	if !strings.Contains(resultOutputText(t, result), "unsupported media type") {
		t.Fatalf("result output = %q, want contains unsupported media type", resultOutputText(t, result))
	}
}

func mustParams(t *testing.T, input any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func resultOutputText(t *testing.T, result types.ToolResult) string {
	t.Helper()
	output, ok := result.Value.Value.(string)
	if !ok {
		t.Fatalf("result output type = %T, want string", result.Value.Value)
	}
	return output
}

func resultPayloadMap(t *testing.T, result types.ToolResult) map[string]any {
	t.Helper()
	payload, ok := result.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("result payload type = %T, want map[string]any", result.Value.Value)
	}
	return payload
}
