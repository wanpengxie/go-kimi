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

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
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
