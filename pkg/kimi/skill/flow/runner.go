package flow

import (
	"context"
	"fmt"
	"strings"
)

const (
	// DefaultMaxMoves limits one flow execution loop to avoid infinite cycles.
	DefaultMaxMoves    = 1000
	decisionRetryLimit = 8
)

// TurnFunc executes one flow node prompt and returns assistant text output.
type TurnFunc func(ctx context.Context, prompt string) (string, error)

// Runner executes one parsed flow graph.
type Runner struct {
	Flow     *Flow
	Name     string
	MaxMoves int
}

// Run executes the flow from BEGIN to END.
func (r Runner) Run(ctx context.Context, runTurn TurnFunc) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runTurn == nil {
		return "", fmt.Errorf("flow runner: turn executor is required")
	}
	if r.Flow == nil {
		return "", fmt.Errorf("flow runner: flow is nil")
	}

	maxMoves := r.MaxMoves
	if maxMoves <= 0 {
		maxMoves = DefaultMaxMoves
	}

	currentID := strings.TrimSpace(r.Flow.BeginID)
	if currentID == "" {
		return "", fmt.Errorf("flow runner: begin id is empty")
	}

	lastOutput := ""
	moves := 0
	for {
		node, ok := r.Flow.Nodes[currentID]
		if !ok {
			return "", fmt.Errorf("flow runner: node %q not found", currentID)
		}
		edges := r.Flow.Outgoing[currentID]

		switch node.Kind {
		case NodeKindEnd:
			return strings.TrimSpace(lastOutput), nil
		case NodeKindBegin:
			if len(edges) == 0 {
				return "", fmt.Errorf("flow runner: BEGIN node %q has no outgoing edges", node.ID)
			}
			currentID = edges[0].Dst
			continue
		}

		if moves >= maxMoves {
			return "", fmt.Errorf("flow runner: reached max moves limit %d", maxMoves)
		}

		nextID, output, err := executeNode(ctx, node, edges, runTurn)
		if err != nil {
			return "", err
		}
		lastOutput = output
		if nextID == "" {
			return strings.TrimSpace(lastOutput), nil
		}
		currentID = nextID
		moves++
	}
}

func executeNode(ctx context.Context, node FlowNode, edges []FlowEdge, runTurn TurnFunc) (string, string, error) {
	if len(edges) == 0 {
		return "", "", fmt.Errorf("flow runner: node %q has no outgoing edges", node.ID)
	}

	basePrompt := buildNodePrompt(node, edges)
	prompt := basePrompt
	retries := 0
	for {
		output, err := runTurn(ctx, prompt)
		if err != nil {
			return "", "", err
		}
		output = strings.TrimSpace(output)
		if node.Kind != NodeKindDecision {
			return edges[0].Dst, output, nil
		}

		choice := ParseChoice(output)
		nextID := matchEdgeByChoice(edges, choice)
		if nextID != "" {
			return nextID, output, nil
		}

		retries++
		if retries >= decisionRetryLimit {
			return "", output, fmt.Errorf("flow runner: invalid choice %q for node %q", choice, node.ID)
		}
		prompt = basePrompt + "\n\nYour last response did not include a valid choice. Reply with one of the choices using <choice>...</choice>."
	}
}

func buildNodePrompt(node FlowNode, edges []FlowEdge) string {
	if node.Kind != NodeKindDecision {
		label := strings.TrimSpace(node.Label)
		if label == "" {
			return node.ID
		}
		return label
	}

	label := strings.TrimSpace(node.Label)
	if label == "" {
		label = node.ID
	}
	choices := make([]string, 0, len(edges))
	for _, edge := range edges {
		if choice := strings.TrimSpace(edge.Label); choice != "" {
			choices = append(choices, choice)
		}
	}

	lines := make([]string, 0, len(choices)+5)
	lines = append(lines, label, "", "Available branches:")
	for _, choice := range choices {
		lines = append(lines, "- "+choice)
	}
	lines = append(lines, "", "Reply with a choice using <choice>...</choice>.")
	return strings.Join(lines, "\n")
}

func matchEdgeByChoice(edges []FlowEdge, choice string) string {
	if strings.TrimSpace(choice) == "" {
		return ""
	}
	for _, edge := range edges {
		if edge.Label == choice {
			return edge.Dst
		}
	}
	return ""
}
