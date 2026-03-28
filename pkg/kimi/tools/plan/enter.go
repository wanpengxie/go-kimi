package plan

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	enterToolName        = "enter_plan_mode"
	enterToolDescription = "Enter plan mode and allocate a plan markdown file path."
)

var enterParameterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var slugWordPoolA = []string{
	"calm", "bold", "swift", "clear", "quiet", "steady",
	"bright", "plain", "sharp", "solid", "brisk", "fresh",
}

var slugWordPoolB = []string{
	"river", "forest", "harbor", "meadow", "summit", "valley",
	"ocean", "canyon", "signal", "bridge", "anchor", "vertex",
}

var slugWordPoolC = []string{
	"draft", "outline", "plan", "review", "scope", "design",
	"check", "notes", "report", "thread", "track", "route",
}

// EnterPlanMode implements the enter_plan_mode tool.
type EnterPlanMode struct {
	WorkDir string
	State   *PlanState

	slugGenerator func() (string, error)
}

// NewEnterPlanMode creates one enter_plan_mode tool.
func NewEnterPlanMode(workDir string, state *PlanState) *EnterPlanMode {
	if state == nil {
		state = NewPlanState()
	}
	return &EnterPlanMode{
		WorkDir:       strings.TrimSpace(workDir),
		State:         state,
		slugGenerator: randomThreeWordSlug,
	}
}

// Name returns the tool name.
func (*EnterPlanMode) Name() string {
	return enterToolName
}

// Description returns the tool description.
func (*EnterPlanMode) Description() string {
	return enterToolDescription
}

// ParameterSchema returns the JSON schema for tool params.
func (*EnterPlanMode) ParameterSchema() json.RawMessage {
	return cloneRawMessage(enterParameterSchema)
}

// Execute enters plan mode and returns the generated plan file path.
func (t *EnterPlanMode) Execute(ctx context.Context, params json.RawMessage) (types.ToolResult, error) {
	if err := decodeEnterParams(params); err != nil {
		return types.ToolResult{}, err
	}

	state := t.planState()
	if state == nil {
		return types.ToolResult{}, errors.New("enter_plan_mode: state is unavailable")
	}

	workDir, err := resolveWorkDir(t.WorkDir)
	if err != nil {
		return types.ToolResult{}, err
	}

	slug, err := t.generateSlug()
	if err != nil {
		return types.ToolResult{}, err
	}
	slug, err = normalizeSlug(slug)
	if err != nil {
		return types.ToolResult{}, err
	}

	planFile := filepath.Join(workDir, ".kimi", "plans", slug+".md")
	if err := state.Enter(planFile); err != nil {
		return types.ToolResult{}, fmt.Errorf("enter_plan_mode: %w", err)
	}

	return buildResult(enterToolName, map[string]any{
		"plan_file": planFile,
		"active":    true,
	}, false), nil
}

func (t *EnterPlanMode) planState() *PlanState {
	if t == nil {
		return nil
	}
	if t.State == nil {
		t.State = NewPlanState()
	}
	return t.State
}

func (t *EnterPlanMode) generateSlug() (string, error) {
	if t == nil || t.slugGenerator == nil {
		return randomThreeWordSlug()
	}
	return t.slugGenerator()
}

func decodeEnterParams(raw json.RawMessage) error {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("enter_plan_mode: decode params: %w", err)
	}
	if len(decoded) > 0 {
		return errors.New("enter_plan_mode: this tool does not accept parameters")
	}
	return nil
}

func normalizeSlug(slug string) (string, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return "", errors.New("enter_plan_mode: generated empty slug")
	}
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("enter_plan_mode: generated invalid slug %q", slug)
	}
	return slug, nil
}

func randomThreeWordSlug() (string, error) {
	wordA, err := randomWord(slugWordPoolA)
	if err != nil {
		return "", err
	}
	wordB, err := randomWord(slugWordPoolB)
	if err != nil {
		return "", err
	}
	wordC, err := randomWord(slugWordPoolC)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{wordA, wordB, wordC}, "-"), nil
}

func randomWord(pool []string) (string, error) {
	if len(pool) == 0 {
		return "", errors.New("enter_plan_mode: empty word pool")
	}

	max := big.NewInt(int64(len(pool)))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("enter_plan_mode: generate random slug word: %w", err)
	}
	return pool[n.Int64()], nil
}
