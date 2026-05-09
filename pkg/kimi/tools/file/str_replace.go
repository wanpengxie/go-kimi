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
	strReplaceToolName        = "str_replace"
	strReplaceToolDescription = "Replace one unique string occurrence in a file."
)

var strReplaceParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "File path to modify"
    },
    "old_string": {
      "type": "string",
      "description": "Original string to replace"
    },
    "new_string": {
      "type": "string",
      "description": "Replacement string"
    }
  },
  "required": ["path", "old_string", "new_string"],
  "additionalProperties": false
}`)

// StrReplace implements the str_replace tool.
type StrReplace struct {
	WorkDir  string
	Approver Approver
}

type strReplaceParams struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// NewStrReplace creates a str_replace tool.
func NewStrReplace(workDir string, approver Approver) *StrReplace {
	return &StrReplace{
		WorkDir:  strings.TrimSpace(workDir),
		Approver: approver,
	}
}

// Name returns the tool name.
func (*StrReplace) Name() string {
	return strReplaceToolName
}

// Description returns the tool description.
func (*StrReplace) Description() string {
	return strReplaceToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*StrReplace) ParameterSchema() json.RawMessage {
	return cloneRawMessage(strReplaceParameterSchema)
}

// Execute replaces exactly one old_string occurrence and writes back to file.
func (t *StrReplace) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	input, err := decodeStrReplaceParams(params)
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

	content, err := os.ReadFile(path)
	if err != nil {
		return buildErrorResult(strReplaceToolName, fmt.Sprintf("read file %q: %v", pathLabel, err)), nil
	}
	original := string(content)
	count := strings.Count(original, input.OldString)
	switch {
	case count == 0:
		return buildErrorResult(strReplaceToolName, "old_string not found"), nil
	case count > 1:
		return buildErrorResult(strReplaceToolName, fmt.Sprintf("old_string is not unique: found %d occurrences", count)), nil
	}

	if t != nil && t.Approver != nil {
		desc := fmt.Sprintf("replace in %s (old_len=%d new_len=%d)", pathLabel, len(input.OldString), len(input.NewString))
		approved, feedback := t.Approver(ctx, strReplaceToolName, desc)
		if !approved {
			return rejectionResult(strReplaceToolName, feedback), nil
		}
	}

	updated := strings.Replace(original, input.OldString, input.NewString, 1)
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if writeErr := os.WriteFile(path, []byte(updated), mode); writeErr != nil {
		return buildErrorResult(strReplaceToolName, fmt.Sprintf("write file %q: %v", pathLabel, writeErr)), nil
	}

	return buildResult(strReplaceToolName, fmt.Sprintf("replaced text in %s", pathLabel), false), nil
}

func decodeStrReplaceParams(raw json.RawMessage) (strReplaceParams, error) {
	var input strReplaceParams
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return strReplaceParams{}, fmt.Errorf("str_replace: decode params: %w", err)
		}
	}

	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return strReplaceParams{}, errors.New("str_replace: path is required")
	}
	if input.OldString == "" {
		return strReplaceParams{}, errors.New("str_replace: old_string is required")
	}
	return input, nil
}
