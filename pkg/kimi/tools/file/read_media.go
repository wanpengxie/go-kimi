package file

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	toolparams "github.com/wanpengxie/go-kimi/pkg/kimi/tools/internal/params"
	"github.com/wanpengxie/go-kimi/pkg/kimi/types"
)

const (
	readMediaToolName        = "read_media_file"
	readMediaToolDescription = "Read one image/video file as a base64 data URL content part."

	defaultMediaMaxBytes = int64(100 * 1024 * 1024)
)

var readMediaParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Image or video file path to read"
    }
  },
  "required": ["path"],
  "additionalProperties": false
}`)

// ReadMediaFile implements the read_media_file tool.
type ReadMediaFile struct {
	WorkDir        string
	SupportsVision bool
	SupportsVideo  bool
	MaxBytes       int64
}

type readMediaParams struct {
	Path string `json:"path"`
}

// NewReadMediaFile creates one read_media_file tool.
func NewReadMediaFile(workDir string, supportsVision bool, supportsVideo bool) *ReadMediaFile {
	return &ReadMediaFile{
		WorkDir:        strings.TrimSpace(workDir),
		SupportsVision: supportsVision,
		SupportsVideo:  supportsVideo,
		MaxBytes:       defaultMediaMaxBytes,
	}
}

// Name returns the tool name.
func (*ReadMediaFile) Name() string {
	return readMediaToolName
}

// Description returns the tool description.
func (*ReadMediaFile) Description() string {
	return readMediaToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*ReadMediaFile) ParameterSchema() json.RawMessage {
	return cloneRawMessage(readMediaParameterSchema)
}

// Execute reads one media file and returns base64 data URL content parts.
func (t *ReadMediaFile) Execute(_ context.Context, params json.RawMessage) (types.ToolResult, error) {
	input, err := decodeReadMediaParams(params)
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

	limit := t.maxBytes()
	info, err := os.Stat(path)
	if err != nil {
		return buildErrorResult(readMediaToolName, fmt.Sprintf("stat file %q: %v", pathLabel, err)), nil
	}
	if !info.Mode().IsRegular() {
		return buildErrorResult(readMediaToolName, fmt.Sprintf("file %q is not a regular file", pathLabel)), nil
	}
	if info.Size() > limit {
		return buildErrorResult(
			readMediaToolName,
			fmt.Sprintf("file %q is larger than %d bytes", pathLabel, limit),
		), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return buildErrorResult(readMediaToolName, fmt.Sprintf("read file %q: %v", pathLabel, err)), nil
	}

	mediaType := detectMediaType(path, data)
	kind := mediaKind(mediaType)
	switch kind {
	case "image":
		if !t.SupportsVision {
			return readMediaSkipResult(pathLabel, mediaType, "model capability vision is disabled"), nil
		}
		part := types.ImageURLPart{
			ImageURL: dataURL(mediaType, data),
		}
		return readMediaSuccessResult(pathLabel, mediaType, len(data), types.ContentParts{part}), nil
	case "video":
		if !t.SupportsVideo {
			return readMediaSkipResult(pathLabel, mediaType, "model capability video_input is disabled"), nil
		}
		part := types.VideoURLPart{
			VideoURL: dataURL(mediaType, data),
		}
		return readMediaSuccessResult(pathLabel, mediaType, len(data), types.ContentParts{part}), nil
	default:
		return buildErrorResult(
			readMediaToolName,
			fmt.Sprintf("file %q has unsupported media type %q", pathLabel, mediaType),
		), nil
	}
}

func decodeReadMediaParams(raw json.RawMessage) (readMediaParams, error) {
	input := readMediaParams{}
	if err := toolparams.DecodeStrict(raw, &input); err != nil {
		return readMediaParams{}, fmt.Errorf("read_media_file: decode params: %w", err)
	}

	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return readMediaParams{}, errors.New("read_media_file: path is required")
	}
	return input, nil
}

func (t *ReadMediaFile) maxBytes() int64 {
	if t != nil && t.MaxBytes > 0 {
		return t.MaxBytes
	}
	return defaultMediaMaxBytes
}

func detectMediaType(path string, data []byte) string {
	mediaType := strings.TrimSpace(http.DetectContentType(data))
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	if normalized == "application/octet-stream" || normalized == "" {
		extType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(path))))
		if extType != "" {
			mediaType = extType
		}
	}

	parsed, _, err := mime.ParseMediaType(mediaType)
	if err == nil {
		parsed = strings.ToLower(strings.TrimSpace(parsed))
		if parsed != "" {
			return parsed
		}
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func mediaKind(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return "image"
	case strings.HasPrefix(mediaType, "video/"):
		return "video"
	default:
		return ""
	}
}

func dataURL(mediaType string, data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mediaType, encoded)
}

func readMediaSuccessResult(path string, mediaType string, sizeBytes int, parts types.ContentParts) types.ToolResult {
	return types.ToolResult{
		Name: readMediaToolName,
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"path":          path,
				"media_type":    mediaType,
				"size_bytes":    sizeBytes,
				"content_parts": parts,
			},
		},
	}
}

func readMediaSkipResult(path string, mediaType string, reason string) types.ToolResult {
	return types.ToolResult{
		Name: readMediaToolName,
		Value: types.ToolReturnValue{
			Value: map[string]any{
				"path":       path,
				"media_type": mediaType,
				"skipped":    true,
				"reason":     strings.TrimSpace(reason),
			},
		},
	}
}
