package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	writeToolName        = "write_file"
	writeToolDescription = "Write or append content to a file, creating parent directories if needed."
)

var writeParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "File path to write"
    },
    "content": {
      "type": "string",
      "description": "Content to write"
    },
    "append": {
      "type": "boolean",
      "default": false,
      "description": "Whether to append instead of overwrite"
    }
  },
  "required": ["path", "content"],
  "additionalProperties": false
}`)

// WriteFile implements the write_file tool.
type WriteFile struct {
	WorkDir  string
	Approver Approver
}

type writeParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append"`
}

// NewWriteFile creates a write_file tool.
func NewWriteFile(workDir string, approver Approver) *WriteFile {
	return &WriteFile{
		WorkDir:  strings.TrimSpace(workDir),
		Approver: approver,
	}
}

// Name returns the tool name.
func (*WriteFile) Name() string {
	return writeToolName
}

// Description returns the tool description.
func (*WriteFile) Description() string {
	return writeToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*WriteFile) ParameterSchema() json.RawMessage {
	return cloneRawMessage(writeParameterSchema)
}

// Execute writes or appends one file.
func (t *WriteFile) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeWriteParams(params)
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
	pathLabel := relativePath(workDir, path)

	if t != nil && t.Approver != nil {
		action := "write"
		if input.Append {
			action = "append"
		}
		desc := fmt.Sprintf("%s %s (%d bytes)", action, pathLabel, len(input.Content))
		approved, feedback := t.Approver(ctx, writeToolName, desc)
		if !approved {
			return rejectionResult(writeToolName, feedback), nil
		}
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return buildErrorResult(writeToolName, fmt.Sprintf("create parent dir for %q: %v", pathLabel, err)), nil
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	op := "wrote"
	if input.Append {
		flags |= os.O_APPEND
		op = "appended"
	} else {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return buildErrorResult(writeToolName, fmt.Sprintf("open file %q: %v", pathLabel, err)), nil
	}

	_, writeErr := file.WriteString(input.Content)
	closeErr := file.Close()
	if writeErr != nil {
		return buildErrorResult(writeToolName, fmt.Sprintf("write file %q: %v", pathLabel, writeErr)), nil
	}
	if closeErr != nil {
		return buildErrorResult(writeToolName, fmt.Sprintf("close file %q: %v", pathLabel, closeErr)), nil
	}

	return buildResult(writeToolName, fmt.Sprintf("%s %d bytes to %s", op, len(input.Content), pathLabel), false), nil
}

func decodeWriteParams(raw json.RawMessage) (writeParams, error) {
	var input writeParams
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return writeParams{}, fmt.Errorf("write_file: decode params: %w", err)
		}
	}
	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return writeParams{}, errors.New("write_file: path is required")
	}
	return input, nil
}
