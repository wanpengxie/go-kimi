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

	corebg "github.com/xiewanpeng/go-kimi/pkg/kimi/background"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	toolName        = "shell"
	toolDescription = "Run one shell command with timeout and output limits."

	defaultTimeoutSeconds = 60
	minTimeoutSeconds     = 1
	maxTimeoutSeconds     = 600

	lineTruncateSuffix   = "...[line-truncated]"
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
	    },
	    "background": {
	      "type": "boolean",
	      "default": false,
	      "description": "Run command in background task manager"
	    }
	  },
	  "required": ["command"],
	  "additionalProperties": false
}`)

// Approver asks for permission before command execution.
type Approver func(ctx context.Context, action, desc string) (bool, string)

// BackgroundManager defines background task creation dependency.
type BackgroundManager interface {
	CreateBashTask(ctx context.Context, spec corebg.TaskSpec) (string, error)
}

// Tool implements the shell core tool.
type Tool struct {
	WorkDir             string
	Approver            Approver
	BackgroundManager   BackgroundManager
	BackgroundSessionID string
}

type executeParams struct {
	Command     string `json:"command"`
	Timeout     int    `json:"timeout"`
	Description string `json:"description"`
	Background  bool   `json:"background"`
}

// New creates one shell tool.
func New(workDir string, approver Approver) *Tool {
	return &Tool{
		WorkDir:  strings.TrimSpace(workDir),
		Approver: approver,
	}
}

// NewWithBackground creates one shell tool with background manager integration.
func NewWithBackground(
	workDir string,
	approver Approver,
	backgroundManager BackgroundManager,
	sessionID string,
) *Tool {
	return &Tool{
		WorkDir:             strings.TrimSpace(workDir),
		Approver:            approver,
		BackgroundManager:   backgroundManager,
		BackgroundSessionID: strings.TrimSpace(sessionID),
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

	if input.Background {
		return t.executeBackground(ctx, input, workDir), nil
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

func (t *Tool) executeBackground(ctx context.Context, input executeParams, workDir string) types.ToolResult {
	if t == nil || t.BackgroundManager == nil {
		return buildResult("shell tool: background manager is not configured", true)
	}

	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = input.Command
	}

	taskID, err := t.BackgroundManager.CreateBashTask(ctx, corebg.TaskSpec{
		SessionID:   strings.TrimSpace(t.BackgroundSessionID),
		Description: description,
		Command:     input.Command,
		WorkDir:     strings.TrimSpace(workDir),
		TimeoutSec:  input.Timeout,
	})
	if err != nil {
		return buildResult(fmt.Sprintf("shell tool: create background task: %v", err), true)
	}

	return types.ToolResult{
		Name: toolName,
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"task_id":           taskID,
				"status":            string(corebg.TaskCreated),
				"run_in_background": true,
				"command":           input.Command,
			},
		},
	}
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
