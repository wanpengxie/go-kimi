package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	toolName        = "shell"
	toolDescription = "Run one shell command with timeout and output limits."

	defaultTimeoutSeconds = 60
	minTimeoutSeconds     = 1
	maxTimeoutSeconds     = 600

	lineTruncateSuffix  = "...[line-truncated]"
	outputTruncateSuffix = "\n...[truncated]"
)

var parameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute"
    },
    "timeout": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "default": 60,
      "description": "Timeout in seconds"
    },
    "description": {
      "type": "string",
      "description": "Optional command description"
    }
  },
  "required": ["command"],
  "additionalProperties": false
}`)

// Approver asks for permission before command execution.
type Approver func(ctx context.Context, action, desc string) (bool, string)

// Tool implements the shell core tool.
type Tool struct {
	WorkDir  string
	Approver Approver
}

type executeParams struct {
	Command     string `json:"command"`
	Timeout     int    `json:"timeout"`
	Description string `json:"description"`
}

// New creates one shell tool.
func New(workDir string, approver Approver) *Tool {
	return &Tool{
		WorkDir:  strings.TrimSpace(workDir),
		Approver: approver,
	}
}

// Name returns the tool name.
func (*Tool) Name() string {
	return toolName
}

// Description returns the tool description.
func (*Tool) Description() string {
	return toolDescription
}

// ParameterSchema returns the JSON schema for shell params.
func (*Tool) ParameterSchema() json.RawMessage {
	return cloneRawMessage(parameterSchema)
}

// Execute runs one bash command and returns merged command output.
func (t *Tool) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	workDir, err := t.resolveWorkDir()
	if err != nil {
		return types.ToolResult{}, err
	}

	if t != nil && t.Approver != nil {
		approved, feedback := t.Approver(ctx, toolName, input.Command)
		if !approved {
			message := "tool call rejected"
			feedback = strings.TrimSpace(feedback)
			if feedback != "" {
				message = message + ": " + feedback
			}
			return buildResult(message, true), nil
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()

	command := exec.CommandContext(runCtx, "bash", "-c", input.Command)
	command.Dir = workDir

	output, runErr := command.CombinedOutput()
	outputText := string(output)

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		timeoutMessage := fmt.Sprintf("command timed out after %d seconds", input.Timeout)
		outputText = joinLines(outputText, timeoutMessage)
		return buildResult(outputText, true), nil
	}

	if runErr != nil {
		if strings.TrimSpace(outputText) == "" {
			outputText = runErr.Error()
		}
		return buildResult(outputText, true), nil
	}

	return buildResult(outputText, false), nil
}

func (t *Tool) resolveWorkDir() (string, error) {
	if t != nil && strings.TrimSpace(t.WorkDir) != "" {
		return strings.TrimSpace(t.WorkDir), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("shell tool: resolve workdir: %w", err)
	}
	return dir, nil
}

func decodeParams(raw json.RawMessage) (executeParams, error) {
	input := executeParams{
		Timeout: defaultTimeoutSeconds,
	}

	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return executeParams{}, fmt.Errorf("shell tool: decode params: %w", err)
		}
	}

	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return executeParams{}, errors.New("shell tool: command is required")
	}
	if input.Timeout == 0 {
		input.Timeout = defaultTimeoutSeconds
	}
	if input.Timeout < minTimeoutSeconds || input.Timeout > maxTimeoutSeconds {
		return executeParams{}, fmt.Errorf(
			"shell tool: timeout must be in range [%d, %d]",
			minTimeoutSeconds,
			maxTimeoutSeconds,
		)
	}
	return input, nil
}

func buildResult(output string, isError bool) types.ToolResult {
	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: limitOutput(output),
		},
		IsError: isError,
	}
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

	idx := 0
	for pos := range text {
		if idx == n {
			return text[:pos]
		}
		idx++
	}
	return text
}

func joinLines(base, extra string) string {
	base = strings.TrimRight(base, "\n")
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n" + extra
	}
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
