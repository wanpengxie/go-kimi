package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	agenttool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/agent"
	bgtools "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/background"
	dmailtool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/dmail"
	filetool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/file"
	plantool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/plan"
	questiontool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/question"
	shelltool "github.com/xiewanpeng/go-kimi/pkg/kimi/tools/shell"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/tools/think"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

type metadataTool interface {
	Name() string
	Description() string
	ParameterSchema() json.RawMessage
}

type metadataCase struct {
	name string
	tool metadataTool
}

func TestToolMetadataNameAndDescriptionAreNonEmpty(t *testing.T) {
	t.Parallel()

	cases := metadataTools(t)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.TrimSpace(tc.tool.Name()); got == "" {
				t.Fatal("Name() returned empty string")
			}
			if got := strings.TrimSpace(tc.tool.Description()); got == "" {
				t.Fatal("Description() returned empty string")
			}
		})
	}
}

func TestToolMetadataParameterSchemaIsValidJSONObject(t *testing.T) {
	t.Parallel()

	cases := metadataTools(t)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema := tc.tool.ParameterSchema()
			if len(schema) == 0 {
				t.Fatal("ParameterSchema() returned empty schema")
			}

			var decoded map[string]any
			if err := json.Unmarshal(schema, &decoded); err != nil {
				t.Fatalf("ParameterSchema() is invalid JSON: %v", err)
			}
			if len(decoded) == 0 {
				t.Fatal("ParameterSchema() decoded to empty object")
			}
			if got, _ := decoded["type"].(string); got != "object" {
				t.Fatalf("schema.type = %q, want object", got)
			}
		})
	}
}

func metadataTools(t *testing.T) []metadataCase {
	t.Helper()

	workDir := t.TempDir()
	hub := wire.NewHub(8)

	return []metadataCase{
		{name: "think", tool: think.New()},
		{name: "shell", tool: shelltool.New(workDir, nil)},
		{name: "read_file", tool: filetool.NewReadFile(workDir)},
		{name: "read_media_file", tool: filetool.NewReadMediaFile(workDir, true, true)},
		{name: "write_file", tool: filetool.NewWriteFile(workDir, nil)},
		{name: "str_replace", tool: filetool.NewStrReplace(workDir, nil)},
		{name: "glob", tool: filetool.NewGlob(workDir)},
		{name: "grep", tool: filetool.NewGrep(workDir)},
		{name: "ask_user_question", tool: questiontool.New(hub, hub, func() bool { return false })},
		{name: "enter_plan_mode", tool: plantool.NewEnterPlanMode(workDir, plantool.NewPlanState())},
		{name: "exit_plan_mode", tool: plantool.NewExitPlanMode(plantool.NewPlanState())},
		{name: "send_dmail", tool: dmailtool.New(nil)},
		{name: "agent", tool: agenttool.New(nil, nil)},
		{name: "task_list", tool: bgtools.NewTaskList(nil)},
		{name: "task_output", tool: bgtools.NewTaskOutput(nil)},
		{name: "task_stop", tool: bgtools.NewTaskStop(nil)},
	}
}
