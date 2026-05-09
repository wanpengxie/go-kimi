package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	readToolName        = "read_file"
	readToolDescription = "Read file content by line range with output limits."

	defaultLineOffset = 1
	defaultReadLines  = 1000
)

var readParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "File path to read"
    },
    "line_offset": {
      "type": "integer",
      "default": 1,
      "minimum": 1,
      "description": "1-based start line"
    },
    "n_lines": {
      "type": "integer",
      "default": 1000,
      "minimum": 1,
      "description": "Number of lines to read"
    }
  },
  "required": ["path"],
  "additionalProperties": false
}`)

// ReadFile implements the read_file tool.
type ReadFile struct {
	WorkDir string
}

type readParams struct {
	Path       string `json:"path"`
	LineOffset int    `json:"line_offset"`
	NLines     int    `json:"n_lines"`
}

// NewReadFile creates a read_file tool.
func NewReadFile(workDir string) *ReadFile {
	return &ReadFile{WorkDir: strings.TrimSpace(workDir)}
}

// Name returns the tool name.
func (*ReadFile) Name() string {
	return readToolName
}

// Description returns the tool description.
func (*ReadFile) Description() string {
	return readToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*ReadFile) ParameterSchema() json.RawMessage {
	return cloneRawMessage(readParameterSchema)
}

// Execute reads one file and returns selected line content.
func (t *ReadFile) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeReadParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	workDir, err := resolveWorkDir(t.WorkDir)
	if err != nil {
		return types.ToolResult{}, err
	}
	path, err := resolvePath(workDir, input.Path)
	if err != nil {
		return types.ToolResult{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return buildErrorResult(readToolName, fmt.Sprintf("read file %q: %v", relativePath(workDir, path), err)), nil
	}

	selected := selectLines(string(content), input.LineOffset, input.NLines)
	return buildResult(readToolName, selected, false), nil
}

func decodeReadParams(raw json.RawMessage) (readParams, error) {
	input := readParams{
		LineOffset: defaultLineOffset,
		NLines:     defaultReadLines,
	}
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return readParams{}, fmt.Errorf("read_file: decode params: %w", err)
		}
	}

	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return readParams{}, errors.New("read_file: path is required")
	}
	if input.LineOffset == 0 {
		input.LineOffset = defaultLineOffset
	}
	if input.LineOffset < 1 {
		return readParams{}, errors.New("read_file: line_offset must be >= 1")
	}
	if input.NLines == 0 {
		input.NLines = defaultReadLines
	}
	if input.NLines < 1 {
		return readParams{}, errors.New("read_file: n_lines must be >= 1")
	}
	return input, nil
}

func selectLines(text string, lineOffset, nLines int) string {
	if nLines < 1 {
		return ""
	}
	lines := strings.Split(text, "\n")
	start := lineOffset - 1
	if start >= len(lines) {
		return ""
	}
	end := min(start+nLines, len(lines))
	return strings.Join(lines[start:end], "\n")
}
