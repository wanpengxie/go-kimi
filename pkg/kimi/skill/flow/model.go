package flow

import (
	"fmt"
	"regexp"
	"strings"
)

// NodeKind identifies one flow node behavior.
type NodeKind string

const (
	NodeKindBegin    NodeKind = "begin"
	NodeKindEnd      NodeKind = "end"
	NodeKindTask     NodeKind = "task"
	NodeKindDecision NodeKind = "decision"
)

// FlowNode is one parsed flowchart node.
type FlowNode struct {
	ID    string
	Label string
	Kind  NodeKind
}

// FlowEdge is one directed edge between flow nodes.
type FlowEdge struct {
	Src   string
	Dst   string
	Label string
}

// Flow is one validated directed graph with BEGIN/END anchors.
type Flow struct {
	Nodes    map[string]FlowNode
	Outgoing map[string][]FlowEdge
	BeginID  string
	EndID    string
}

// ParseError is raised when a flow source cannot be parsed.
type ParseError struct {
	Message string
}

func (e *ParseError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ValidationError is raised when one parsed graph violates flow rules.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var choiceRe = regexp.MustCompile(`<choice>([^<]*)</choice>`)

// ParseChoice extracts the latest <choice>...</choice> value from one model response.
func ParseChoice(text string) string {
	matches := choiceRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(matches[len(matches)-1][1])
}

// Validate checks flow topology and returns begin/end ids.
func Validate(nodes map[string]FlowNode, outgoing map[string][]FlowEdge) (string, string, error) {
	beginIDs := make([]string, 0, 1)
	endIDs := make([]string, 0, 1)
	for _, node := range nodes {
		switch node.Kind {
		case NodeKindBegin:
			beginIDs = append(beginIDs, node.ID)
		case NodeKindEnd:
			endIDs = append(endIDs, node.ID)
		}
	}

	if len(beginIDs) != 1 {
		return "", "", &ValidationError{Message: fmt.Sprintf("Expected exactly one BEGIN node, found %d", len(beginIDs))}
	}
	if len(endIDs) != 1 {
		return "", "", &ValidationError{Message: fmt.Sprintf("Expected exactly one END node, found %d", len(endIDs))}
	}

	beginID := beginIDs[0]
	endID := endIDs[0]

	reachable := make(map[string]struct{}, len(nodes))
	queue := []string{beginID}
	for len(queue) > 0 {
		nodeID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, ok := reachable[nodeID]; ok {
			continue
		}
		reachable[nodeID] = struct{}{}
		for _, edge := range outgoing[nodeID] {
			if _, ok := reachable[edge.Dst]; !ok {
				queue = append(queue, edge.Dst)
			}
		}
	}

	for _, node := range nodes {
		if _, ok := reachable[node.ID]; !ok {
			continue
		}
		edges := outgoing[node.ID]
		if len(edges) <= 1 {
			continue
		}

		seen := make(map[string]struct{}, len(edges))
		for _, edge := range edges {
			label := strings.TrimSpace(edge.Label)
			if label == "" {
				return "", "", &ValidationError{Message: fmt.Sprintf("Node %q has an unlabeled edge", node.ID)}
			}
			if _, dup := seen[label]; dup {
				return "", "", &ValidationError{Message: fmt.Sprintf("Node %q has duplicate edge labels", node.ID)}
			}
			seen[label] = struct{}{}
		}
	}

	if _, ok := reachable[endID]; !ok {
		return "", "", &ValidationError{Message: "END node is not reachable from BEGIN"}
	}
	return beginID, endID, nil
}

type nodeDef struct {
	node     FlowNode
	explicit bool
}

func inferDecisionNodes(nodes map[string]FlowNode, outgoing map[string][]FlowEdge) map[string]FlowNode {
	updated := make(map[string]FlowNode, len(nodes))
	for nodeID, node := range nodes {
		kind := node.Kind
		if kind == NodeKindTask && len(outgoing[nodeID]) > 1 {
			kind = NodeKindDecision
		}
		node.Kind = kind
		updated[nodeID] = node
	}
	return updated
}

func addNode(
	nodes map[string]nodeDef,
	nodeID string,
	label string,
	explicit bool,
	lineNo int,
) (FlowNode, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return FlowNode{}, lineError(lineNo, "Expected node id")
	}
	if !explicit {
		label = nodeID
	}
	if label == "" {
		return FlowNode{}, lineError(lineNo, "Node label cannot be empty")
	}
	labelNorm := strings.ToLower(strings.TrimSpace(label))
	kind := NodeKindTask
	switch labelNorm {
	case "begin":
		kind = NodeKindBegin
	case "end":
		kind = NodeKindEnd
	}

	node := FlowNode{ID: nodeID, Label: label, Kind: kind}
	existing, ok := nodes[nodeID]
	if !ok {
		nodes[nodeID] = nodeDef{node: node, explicit: explicit}
		return node, nil
	}

	if existing.node == node {
		return existing.node, nil
	}
	if !explicit && existing.explicit {
		return existing.node, nil
	}
	if explicit && !existing.explicit {
		nodes[nodeID] = nodeDef{node: node, explicit: true}
		return node, nil
	}

	return FlowNode{}, lineError(lineNo, fmt.Sprintf("Conflicting definition for node %q", nodeID))
}

func lineError(lineNo int, message string) error {
	return &ParseError{Message: fmt.Sprintf("Line %d: %s", lineNo, message)}
}
