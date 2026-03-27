package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	lineTruncateSuffix   = "...[line-truncated]"
	outputTruncateSuffix = "\n...[truncated]"
)

// Approver asks for permission before mutating file operations.
type Approver func(ctx context.Context, action, desc string) (bool, string)

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func resolveWorkDir(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("file tool: resolve workdir: %w", err)
		}
		workDir = cwd
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("file tool: resolve abs workdir: %w", err)
	}
	return filepath.Clean(absWorkDir), nil
}

func resolvePath(workDir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("file tool: path is required")
	}

	absWorkDir, err := filepath.Abs(filepath.Clean(workDir))
	if err != nil {
		return "", fmt.Errorf("file tool: resolve abs workdir: %w", err)
	}

	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(absWorkDir, path))
	}

	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("file tool: resolve abs path: %w", err)
	}

	evalWorkDir, err := evalPathWithExistingParent(absWorkDir)
	if err != nil {
		return "", fmt.Errorf("file tool: eval workdir symlinks: %w", err)
	}

	evalResolved, err := evalPathWithExistingParent(absResolved)
	if err != nil {
		return "", fmt.Errorf("file tool: eval path symlinks: %w", err)
	}

	if evalResolved != evalWorkDir && !strings.HasPrefix(evalResolved, evalWorkDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("file tool: path %q escapes workdir", path)
	}
	return absResolved, nil
}

// evalPathWithExistingParent resolves symlinks on the longest existing parent
// so paths that don't exist yet can still be validated against the sandbox.
func evalPathWithExistingParent(path string) (string, error) {
	path = filepath.Clean(path)
	evaluated, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(evaluated), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	missing := make([]string, 0, 4)
	probe := path
	for {
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}

		missing = append(missing, filepath.Base(probe))
		probe = parent

		evaluated, evalErr := filepath.EvalSymlinks(probe)
		if evalErr == nil {
			rebuilt := filepath.Clean(evaluated)
			for i := len(missing) - 1; i >= 0; i-- {
				rebuilt = filepath.Join(rebuilt, missing[i])
			}
			return rebuilt, nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
	}
}

func relativePath(workDir, target string) string {
	if strings.TrimSpace(workDir) == "" {
		return filepath.ToSlash(filepath.Clean(target))
	}
	rel, err := filepath.Rel(workDir, target)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(target))
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func buildResult(toolName, output string, isError bool) types.ToolResult {
	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: limitOutput(output),
		},
		IsError: isError,
	}
}

func buildErrorResult(toolName, message string) types.ToolResult {
	return buildResult(toolName, message, true)
}

func rejectionResult(toolName, feedback string) types.ToolResult {
	message := "tool call rejected"
	feedback = strings.TrimSpace(feedback)
	if feedback != "" {
		message += ": " + feedback
	}
	return buildErrorResult(toolName, message)
}

func limitOutput(output string) string {
	limitedLines := truncateEachLine(output, tools.MaxLineLengthChars)
	return truncateWithSuffix(limitedLines, tools.MaxOutputChars, outputTruncateSuffix)
}

func truncateEachLine(text string, limit int) string {
	if limit <= 0 || text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = truncateWithSuffix(lines[i], limit, lineTruncateSuffix)
	}
	return strings.Join(lines, "\n")
}

func truncateWithSuffix(text string, limit int, suffix string) string {
	if limit <= 0 {
		return ""
	}

	if utf8.RuneCountInString(text) <= limit {
		return text
	}

	suffixRunes := utf8.RuneCountInString(suffix)
	if suffixRunes >= limit {
		return firstNRunes(text, limit)
	}

	keep := limit - suffixRunes
	return firstNRunes(text, keep) + suffix
}

func firstNRunes(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= n {
		return text
	}

	i := 0
	for idx := range text {
		if i == n {
			return text[:idx]
		}
		i++
	}
	return text
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
