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
	if workDir != "" {
		return workDir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("file tool: resolve workdir: %w", err)
	}
	return cwd, nil
}

func resolvePath(workDir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("file tool: path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(workDir, path)), nil
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
