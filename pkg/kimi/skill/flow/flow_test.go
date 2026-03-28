package flow

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMermaidBasic(t *testing.T) {
	t.Parallel()

	graph, err := ParseMermaidFlowchart(strings.Join([]string{
		"flowchart TD",
		"A([BEGIN]) --> B[Search stdrc]",
		"B --> C{Enough?}",
		"C -->|yes| D([END])",
		"C -->|no| B",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMermaidFlowchart() error = %v", err)
	}

	if got, want := graph.BeginID, "A"; got != want {
		t.Fatalf("BeginID = %q, want %q", got, want)
	}
	if got, want := graph.EndID, "D"; got != want {
		t.Fatalf("EndID = %q, want %q", got, want)
	}
	if got := graph.Nodes["C"].Kind; got != NodeKindDecision {
		t.Fatalf("node C kind = %q, want decision", got)
	}
	if len(graph.Outgoing["C"]) != 2 {
		t.Fatalf("len(outgoing[C]) = %d, want 2", len(graph.Outgoing["C"]))
	}
}

func TestParseMermaidImplicitNodes(t *testing.T) {
	t.Parallel()

	graph, err := ParseMermaidFlowchart("flowchart TD\nBEGIN --> TASK\nTASK --> END")
	if err != nil {
		t.Fatalf("ParseMermaidFlowchart() error = %v", err)
	}
	if graph.Nodes["BEGIN"].Kind != NodeKindBegin {
		t.Fatalf("BEGIN kind = %q, want begin", graph.Nodes["BEGIN"].Kind)
	}
	if graph.Nodes["END"].Kind != NodeKindEnd {
		t.Fatalf("END kind = %q, want end", graph.Nodes["END"].Kind)
	}
}

func TestParseMermaidQuotedLabel(t *testing.T) {
	t.Parallel()

	graph, err := ParseMermaidFlowchart(strings.Join([]string{
		"flowchart TD",
		`A(["BEGIN"]) --> B["hello | world"]`,
		"B --> C([END])",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMermaidFlowchart() error = %v", err)
	}
	if got, want := graph.Nodes["B"].Label, "hello | world"; got != want {
		t.Fatalf("node B label = %q, want %q", got, want)
	}
}

func TestParseMermaidRequiresLabelsOnDecisionEdges(t *testing.T) {
	t.Parallel()

	_, err := ParseMermaidFlowchart(strings.Join([]string{
		"flowchart TD",
		"A([BEGIN]) --> B[Pick]",
		"B --> C([END])",
		"B --> D([END])",
	}, "\n"))
	if err == nil {
		t.Fatal("ParseMermaidFlowchart() error = nil, want validation error")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
}

func TestParseMermaidIgnoresStyle(t *testing.T) {
	t.Parallel()

	graph, err := ParseMermaidFlowchart(strings.Join([]string{
		"flowchart TB",
		"classDef highlight fill:#f9f,stroke:#333,stroke-width:2px;",
		"A([BEGIN]) --> B[Working tree clean?]",
		"B -- yes --> C{Prep PR}",
		"B -- no --> D([END])",
		"C --> D",
		"class B highlight",
		"style C fill:#bbf",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMermaidFlowchart() error = %v", err)
	}
	if graph.Nodes["B"].Kind != NodeKindDecision {
		t.Fatalf("node B kind = %q, want decision", graph.Nodes["B"].Kind)
	}
}

func TestParseD2TypicalExample(t *testing.T) {
	t.Parallel()

	graph, err := ParseD2Flowchart(strings.Join([]string{
		`a: "append a random line to file test.txt"`,
		"a.shape: rectangle",
		"a.foo.bar",
		`b: "does test.txt contain more than 3 lines?" {`,
		"  sub1 -> sub2",
		"}",
		"BEGIN -> a -> b",
		"b -> a: no",
		"not_used",
		"b -> END: yes",
		"b -> END: yes2",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseD2Flowchart() error = %v", err)
	}

	if got := graph.Nodes["b"].Kind; got != NodeKindDecision {
		t.Fatalf("node b kind = %q, want decision", got)
	}
	if got := graph.Nodes["a"].Label; got != "append a random line to file test.txt" {
		t.Fatalf("node a label = %q", got)
	}
}

func TestParseD2MarkdownBlockLabel(t *testing.T) {
	t.Parallel()

	graph, err := ParseD2Flowchart(strings.Join([]string{
		"BEGIN -> explanation -> END",
		"explanation: |md",
		"  # Header",
		"  - first",
		"  - second",
		"|",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseD2Flowchart() error = %v", err)
	}
	if got, want := graph.Nodes["explanation"].Label, "# Header\n- first\n- second"; got != want {
		t.Fatalf("node explanation label = %q, want %q", got, want)
	}
}

func TestParseD2MarkdownBlockUnclosed(t *testing.T) {
	t.Parallel()

	_, err := ParseD2Flowchart(strings.Join([]string{
		"BEGIN -> note -> END",
		"note: |md",
		"  missing terminator",
	}, "\n"))
	if err == nil {
		t.Fatal("ParseD2Flowchart() error = nil, want parse error")
	}

	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("error type = %T, want *ParseError", err)
	}
}

func TestParseChoiceLastMatch(t *testing.T) {
	t.Parallel()

	if got, want := ParseChoice("Answer <choice>a</choice> <choice>b</choice>"), "b"; got != want {
		t.Fatalf("ParseChoice() = %q, want %q", got, want)
	}
	if got := ParseChoice("No choice tag"); got != "" {
		t.Fatalf("ParseChoice() = %q, want empty", got)
	}
}

func TestValidateRejectsEdgeTargetingBegin(t *testing.T) {
	t.Parallel()

	nodes := map[string]FlowNode{
		"BEGIN": {ID: "BEGIN", Label: "BEGIN", Kind: NodeKindBegin},
		"A":     {ID: "A", Label: "task", Kind: NodeKindTask},
		"END":   {ID: "END", Label: "END", Kind: NodeKindEnd},
	}
	outgoing := map[string][]FlowEdge{
		"BEGIN": {{Src: "BEGIN", Dst: "A"}},
		"A": {
			{Src: "A", Dst: "BEGIN"},
			{Src: "A", Dst: "END", Label: "done"},
		},
		"END": nil,
	}

	_, _, err := Validate(nodes, outgoing)
	if err == nil {
		t.Fatal("Validate() error = nil, want BEGIN destination validation error")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if !strings.Contains(validationErr.Error(), "cannot be a destination") {
		t.Fatalf("Validate() error = %v, want BEGIN destination detail", validationErr)
	}
}
