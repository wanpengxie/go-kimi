package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	grepToolName        = "grep"
	grepToolDescription = "Search text in one file or directory with optional recursive traversal."
)

var grepParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Regular expression pattern"
    },
    "path": {
      "type": "string",
      "description": "Target file or directory path"
    },
    "recursive": {
      "type": "boolean",
      "default": true,
      "description": "Recursively walk subdirectories when path is a directory"
    },
    "context_lines": {
      "type": "integer",
      "default": 0,
      "minimum": 0,
      "description": "Number of surrounding context lines"
    }
  },
  "required": ["pattern", "path"],
  "additionalProperties": false
}`)

// Grep implements the grep file tool.
type Grep struct {
	WorkDir string
}

type grepParams struct {
	Pattern      string
	Path         string
	Recursive    bool
	ContextLines int
}

// NewGrep creates a grep tool.
func NewGrep(workDir string) *Grep {
	return &Grep{WorkDir: strings.TrimSpace(workDir)}
}

// Name returns the tool name.
func (*Grep) Name() string {
	return grepToolName
}

// Description returns the tool description.
func (*Grep) Description() string {
	return grepToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*Grep) ParameterSchema() json.RawMessage {
	return cloneRawMessage(grepParameterSchema)
}

// Execute searches files for lines matching the requested regular expression.
func (t *Grep) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeGrepParams(params)
	if err != nil {
		return types.ToolResult{}, err
	}

	re, err := regexp.Compile(input.Pattern)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("grep: compile pattern: %w", err)
	}

	workDir, err := resolveWorkDir(t.WorkDir)
	if err != nil {
		return types.ToolResult{}, err
	}
	targetPath, err := resolvePath(workDir, input.Path)
	if err != nil {
		return types.ToolResult{}, err
	}

	files, err := collectGrepFiles(targetPath, input.Recursive)
	if err != nil {
		return buildErrorResult(grepToolName, fmt.Sprintf("grep path %q: %v", relativePath(workDir, targetPath), err)), nil
	}

	matches := make([]string, 0, 64)
	for i := range files {
		lines, grepErr := grepFile(files[i], workDir, re, input.ContextLines)
		if grepErr != nil {
			return buildErrorResult(grepToolName, fmt.Sprintf("grep file %q: %v", relativePath(workDir, files[i]), grepErr)), nil
		}
		matches = append(matches, lines...)
	}

	return buildResult(grepToolName, strings.Join(matches, "\n"), false), nil
}

func decodeGrepParams(raw json.RawMessage) (grepParams, error) {
	input := grepParams{
		Recursive:    true,
		ContextLines: 0,
	}
	if text := strings.TrimSpace(string(raw)); text != "" && text != "null" {
		var decoded struct {
			Pattern      string `json:"pattern"`
			Path         string `json:"path"`
			Recursive    *bool  `json:"recursive"`
			ContextLines *int   `json:"context_lines"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return grepParams{}, fmt.Errorf("grep: decode params: %w", err)
		}
		input.Pattern = decoded.Pattern
		input.Path = decoded.Path
		if decoded.Recursive != nil {
			input.Recursive = *decoded.Recursive
		}
		if decoded.ContextLines != nil {
			input.ContextLines = *decoded.ContextLines
		}
	}

	input.Pattern = strings.TrimSpace(input.Pattern)
	if input.Pattern == "" {
		return grepParams{}, errors.New("grep: pattern is required")
	}
	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return grepParams{}, errors.New("grep: path is required")
	}
	if input.ContextLines < 0 {
		return grepParams{}, errors.New("grep: context_lines must be >= 0")
	}
	return input, nil
}

func collectGrepFiles(path string, recursive bool) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return []string{path}, nil
	}

	files := make([]string, 0, 16)
	if recursive {
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			files = append(files, current)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return nil, readErr
		}
		for i := range entries {
			if entries[i].IsDir() {
				continue
			}
			files = append(files, filepath.Join(path, entries[i].Name()))
		}
	}

	sort.Strings(files)
	return files, nil
}

func grepFile(path, workDir string, pattern *regexp.Regexp, contextLines int) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")
	matchedLineIdx := make([]int, 0)
	for i := range lines {
		if pattern.MatchString(lines[i]) {
			matchedLineIdx = append(matchedLineIdx, i)
		}
	}
	if len(matchedLineIdx) == 0 {
		return nil, nil
	}

	include := make(map[int]struct{}, len(matchedLineIdx))
	for i := range matchedLineIdx {
		start := max(0, matchedLineIdx[i]-contextLines)
		end := min(len(lines)-1, matchedLineIdx[i]+contextLines)
		for line := start; line <= end; line++ {
			include[line] = struct{}{}
		}
	}

	indexes := make([]int, 0, len(include))
	for idx := range include {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	pathLabel := relativePath(workDir, path)
	result := make([]string, 0, len(indexes))
	for i := range indexes {
		idx := indexes[i]
		result = append(result, fmt.Sprintf("%s:%d: %s", pathLabel, idx+1, strings.TrimRight(lines[idx], "\r")))
	}
	return result, nil
}
