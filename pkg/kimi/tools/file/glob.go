package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	globToolName        = "glob"
	globToolDescription = "Find files by glob pattern and return matching relative paths."
	defaultGlobLimit    = 1000
)

var globParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Glob pattern to match"
    },
    "limit": {
      "type": "integer",
      "default": 1000,
      "minimum": 1,
      "description": "Maximum number of matched paths"
    }
  },
  "required": ["pattern"],
  "additionalProperties": false
}`)

// Glob implements the glob file tool.
type Glob struct {
	WorkDir string
}

type globParams struct {
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
}

// NewGlob creates a glob tool.
func NewGlob(workDir string) *Glob {
	return &Glob{WorkDir: strings.TrimSpace(workDir)}
}

// Name returns the tool name.
func (*Glob) Name() string {
	return globToolName
}

// Description returns the tool description.
func (*Glob) Description() string {
	return globToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*Glob) ParameterSchema() json.RawMessage {
	return cloneRawMessage(globParameterSchema)
}

// Execute resolves glob matches under the configured workdir.
func (t *Glob) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeGlobParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	workDir, err := resolveWorkDir(t.WorkDir)
	if err != nil {
		return types.ToolResult{}, err
	}

	pattern, err := resolvePath(workDir, input.Pattern)
	if err != nil {
		return types.ToolResult{}, err
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("glob: invalid pattern: %w", err)
	}
	if len(matches) == 0 {
		return buildResult(globToolName, "", false), nil
	}

	sort.Strings(matches)
	limit := min(input.Limit, len(matches))
	result := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, relativePath(workDir, matches[i]))
	}

	return buildResult(globToolName, strings.Join(result, "\n"), false), nil
}

func decodeGlobParams(raw json.RawMessage) (globParams, error) {
	input := globParams{Limit: defaultGlobLimit}
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return globParams{}, fmt.Errorf("glob: decode params: %w", err)
		}
	}
	input.Pattern = strings.TrimSpace(input.Pattern)
	if input.Pattern == "" {
		return globParams{}, errors.New("glob: pattern is required")
	}
	if input.Limit == 0 {
		input.Limit = defaultGlobLimit
	}
	if input.Limit < 1 {
		return globParams{}, errors.New("glob: limit must be >= 1")
	}
	return input, nil
}
