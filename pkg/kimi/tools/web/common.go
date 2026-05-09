package web

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/wanpengxie/go-kimi/pkg/kimi/tools"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	lineTruncateSuffix   = "...[line-truncated]"
	outputTruncateSuffix = "\n...[truncated]"
)

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
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
	return buildResult(toolName, strings.TrimSpace(message), true)
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
