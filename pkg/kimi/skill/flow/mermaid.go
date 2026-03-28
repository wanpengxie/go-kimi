package flow

import (
	"regexp"
	"strings"
)

type mermaidNodeSpec struct {
	nodeID   string
	label    string
	explicit bool
}

var (
	mermaidNodeIDRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*`)
	mermaidHeaderRe    = regexp.MustCompile(`(?i)^(flowchart|graph)\b`)
	mermaidStyleToken  = regexp.MustCompile(`:::[A-Za-z0-9_-]+`)
	mermaidPipeLabelRe = regexp.MustCompile(`\|([^|]*)\|`)
	mermaidEdgeLabelRe = regexp.MustCompile(`--\s*([^>-][^>]*)\s*-->`)
	mermaidArrowRe     = regexp.MustCompile(`[-.=]+>`)
)

var mermaidShapeClosers = map[byte]byte{
	'[': ']',
	'(': ')',
	'{': '}',
}

// ParseMermaidFlowchart parses one Mermaid flowchart to a validated Flow.
func ParseMermaidFlowchart(text string) (*Flow, error) {
	nodes := make(map[string]nodeDef)
	outgoing := make(map[string][]FlowEdge)

	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	for index, rawLine := range strings.Split(normalized, "\n") {
		lineNo := index + 1
		line := strings.TrimSpace(stripMermaidComment(rawLine))
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if mermaidHeaderRe.MatchString(line) || isMermaidStyleLine(line) {
			continue
		}
		line = stripMermaidStyleTokens(line)

		srcSpec, label, dstSpec, ok, err := tryParseMermaidEdgeLine(line, lineNo)
		if err != nil {
			return nil, err
		}
		if ok {
			srcNode, err := addNode(nodes, srcSpec.nodeID, srcSpec.label, srcSpec.explicit, lineNo)
			if err != nil {
				return nil, err
			}
			dstNode, err := addNode(nodes, dstSpec.nodeID, dstSpec.label, dstSpec.explicit, lineNo)
			if err != nil {
				return nil, err
			}
			edge := FlowEdge{Src: srcNode.ID, Dst: dstNode.ID, Label: label}
			outgoing[edge.Src] = append(outgoing[edge.Src], edge)
			if _, exists := outgoing[edge.Dst]; !exists {
				outgoing[edge.Dst] = nil
			}
			continue
		}

		nodeSpec, parsed, err := tryParseMermaidNodeLine(line, lineNo)
		if err != nil {
			return nil, err
		}
		if !parsed {
			continue
		}
		if _, err := addNode(nodes, nodeSpec.nodeID, nodeSpec.label, nodeSpec.explicit, lineNo); err != nil {
			return nil, err
		}
	}

	flowNodes := make(map[string]FlowNode, len(nodes))
	for nodeID, def := range nodes {
		flowNodes[nodeID] = def.node
		if _, exists := outgoing[nodeID]; !exists {
			outgoing[nodeID] = nil
		}
	}

	flowNodes = inferDecisionNodes(flowNodes, outgoing)
	beginID, endID, err := Validate(flowNodes, outgoing)
	if err != nil {
		return nil, err
	}
	return &Flow{Nodes: flowNodes, Outgoing: outgoing, BeginID: beginID, EndID: endID}, nil
}

func tryParseMermaidEdgeLine(line string, lineNo int) (mermaidNodeSpec, string, mermaidNodeSpec, bool, error) {
	srcSpec, idx, err := parseMermaidNodeToken(line, 0, lineNo)
	if err != nil {
		return mermaidNodeSpec{}, "", mermaidNodeSpec{}, false, nil
	}

	normalized, label := normalizeMermaidEdgeLine(line)
	idx = skipWhitespace(normalized, idx)
	if !strings.Contains(normalized[idx:], ">") {
		offset := strings.Index(normalized[idx:], "---")
		if offset < 0 {
			return mermaidNodeSpec{}, "", mermaidNodeSpec{}, false, nil
		}
		start := idx + offset
		normalized = normalized[:start] + "-->" + normalized[start+3:]
	}

	normalized = mermaidArrowRe.ReplaceAllString(normalized, "-->")
	arrowIdx := strings.LastIndex(normalized, ">")
	if arrowIdx < 0 {
		return mermaidNodeSpec{}, "", mermaidNodeSpec{}, false, nil
	}
	dstStart := skipWhitespace(normalized, arrowIdx+1)
	dstSpec, _, err := parseMermaidNodeToken(normalized, dstStart, lineNo)
	if err != nil {
		return mermaidNodeSpec{}, "", mermaidNodeSpec{}, false, nil
	}
	return srcSpec, label, dstSpec, true, nil
}

func tryParseMermaidNodeLine(line string, lineNo int) (mermaidNodeSpec, bool, error) {
	nodeSpec, _, err := parseMermaidNodeToken(line, 0, lineNo)
	if err != nil {
		return mermaidNodeSpec{}, false, nil
	}
	return nodeSpec, true, nil
}

func parseMermaidNodeToken(line string, idx int, lineNo int) (mermaidNodeSpec, int, error) {
	if idx >= len(line) {
		return mermaidNodeSpec{}, idx, lineError(lineNo, "Expected node id")
	}

	matched := mermaidNodeIDRe.FindString(line[idx:])
	if matched == "" {
		return mermaidNodeSpec{}, idx, lineError(lineNo, "Expected node id")
	}
	nodeID := matched
	idx += len(matched)

	if idx >= len(line) {
		return mermaidNodeSpec{nodeID: nodeID, label: "", explicit: false}, idx, nil
	}
	closeChar, hasShape := mermaidShapeClosers[line[idx]]
	if !hasShape {
		return mermaidNodeSpec{nodeID: nodeID, label: "", explicit: false}, idx, nil
	}
	idx++
	label, nextIdx, err := parseMermaidLabel(line, idx, closeChar, lineNo)
	if err != nil {
		return mermaidNodeSpec{}, idx, err
	}
	return mermaidNodeSpec{nodeID: nodeID, label: label, explicit: true}, nextIdx, nil
}

func parseMermaidLabel(line string, idx int, closeChar byte, lineNo int) (string, int, error) {
	if idx >= len(line) {
		return "", idx, lineError(lineNo, "Expected node label")
	}

	if closeChar == ')' && line[idx] == '[' {
		label, nextIdx, err := parseMermaidLabel(line, idx+1, ']', lineNo)
		if err != nil {
			return "", idx, err
		}
		nextIdx = skipWhitespace(line, nextIdx)
		if nextIdx >= len(line) || line[nextIdx] != ')' {
			return "", idx, lineError(lineNo, "Unclosed node label")
		}
		return label, nextIdx + 1, nil
	}

	if line[idx] == '"' {
		idx++
		var builder strings.Builder
		for idx < len(line) {
			ch := line[idx]
			if ch == '"' {
				idx++
				idx = skipWhitespace(line, idx)
				if idx >= len(line) || line[idx] != closeChar {
					return "", idx, lineError(lineNo, "Unclosed node label")
				}
				return builder.String(), idx + 1, nil
			}
			if ch == '\\' && idx+1 < len(line) {
				builder.WriteByte(line[idx+1])
				idx += 2
				continue
			}
			builder.WriteByte(ch)
			idx++
		}
		return "", idx, lineError(lineNo, "Unclosed quoted label")
	}

	closeIdx := strings.IndexByte(line[idx:], closeChar)
	if closeIdx < 0 {
		return "", idx, lineError(lineNo, "Unclosed node label")
	}
	closeIdx += idx
	label := strings.TrimSpace(line[idx:closeIdx])
	if label == "" {
		return "", idx, lineError(lineNo, "Node label cannot be empty")
	}
	return label, closeIdx + 1, nil
}

func normalizeMermaidEdgeLine(line string) (string, string) {
	normalized := line
	label := ""

	if loc := mermaidPipeLabelRe.FindStringSubmatchIndex(normalized); loc != nil {
		label = strings.TrimSpace(normalized[loc[2]:loc[3]])
		normalized = normalized[:loc[0]] + normalized[loc[1]:]
	}
	if label == "" {
		if loc := mermaidEdgeLabelRe.FindStringSubmatchIndex(normalized); loc != nil {
			label = strings.TrimSpace(normalized[loc[2]:loc[3]])
			normalized = normalized[:loc[0]] + "-->" + normalized[loc[1]:]
		}
	}
	return normalized, label
}

func stripMermaidComment(line string) string {
	if idx := strings.Index(line, "%%"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func isMermaidStyleLine(line string) bool {
	lowered := strings.ToLower(strings.TrimSpace(line))
	if lowered == "end" {
		return true
	}
	return strings.HasPrefix(lowered, "classdef ") ||
		strings.HasPrefix(lowered, "class ") ||
		strings.HasPrefix(lowered, "style ") ||
		strings.HasPrefix(lowered, "linkstyle ") ||
		strings.HasPrefix(lowered, "click ") ||
		strings.HasPrefix(lowered, "subgraph ") ||
		strings.HasPrefix(lowered, "direction ")
}

func stripMermaidStyleTokens(line string) string {
	return mermaidStyleToken.ReplaceAllString(line, "")
}

func skipWhitespace(text string, idx int) int {
	for idx < len(text) {
		switch text[idx] {
		case ' ', '\t', '\n', '\r':
			idx++
		default:
			return idx
		}
	}
	return idx
}
