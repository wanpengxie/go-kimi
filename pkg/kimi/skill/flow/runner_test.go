package flow

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerExecutesDecisionFlow(t *testing.T) {
	t.Parallel()

	graph := &Flow{
		Nodes: map[string]FlowNode{
			"BEGIN": {ID: "BEGIN", Label: "BEGIN", Kind: NodeKindBegin},
			"A":     {ID: "A", Label: "Step A", Kind: NodeKindTask},
			"B":     {ID: "B", Label: "Need continue?", Kind: NodeKindDecision},
			"C":     {ID: "C", Label: "Step C", Kind: NodeKindTask},
			"END":   {ID: "END", Label: "END", Kind: NodeKindEnd},
		},
		Outgoing: map[string][]FlowEdge{
			"BEGIN": {{Src: "BEGIN", Dst: "A"}},
			"A":     {{Src: "A", Dst: "B"}},
			"B": {
				{Src: "B", Dst: "A", Label: "retry"},
				{Src: "B", Dst: "C", Label: "done"},
			},
			"C":   {{Src: "C", Dst: "END"}},
			"END": nil,
		},
		BeginID: "BEGIN",
		EndID:   "END",
	}

	outputs := []string{"work", "<choice>done</choice>", "finished"}
	idx := 0

	runner := Runner{Flow: graph}
	final, err := runner.Run(context.Background(), func(_ context.Context, prompt string) (string, error) {
		if idx >= len(outputs) {
			t.Fatalf("unexpected extra prompt: %q", prompt)
		}
		out := outputs[idx]
		idx++
		return out, nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := final, "finished"; got != want {
		t.Fatalf("final output = %q, want %q", got, want)
	}
}

func TestRunnerRepromptsInvalidDecisionChoice(t *testing.T) {
	t.Parallel()

	graph := &Flow{
		Nodes: map[string]FlowNode{
			"BEGIN": {ID: "BEGIN", Label: "BEGIN", Kind: NodeKindBegin},
			"B":     {ID: "B", Label: "Pick branch", Kind: NodeKindDecision},
			"END":   {ID: "END", Label: "END", Kind: NodeKindEnd},
		},
		Outgoing: map[string][]FlowEdge{
			"BEGIN": {{Src: "BEGIN", Dst: "B"}},
			"B": {
				{Src: "B", Dst: "END", Label: "ok"},
				{Src: "B", Dst: "B", Label: "retry"},
			},
			"END": nil,
		},
		BeginID: "BEGIN",
		EndID:   "END",
	}

	var prompts []string
	outputs := []string{"missing tag", "<choice>ok</choice>"}
	idx := 0

	runner := Runner{Flow: graph}
	_, err := runner.Run(context.Background(), func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		out := outputs[idx]
		idx++
		return out, nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompt count = %d, want 2", len(prompts))
	}
	if !strings.Contains(prompts[1], "did not include a valid choice") {
		t.Fatalf("second prompt = %q, want retry hint", prompts[1])
	}
}
