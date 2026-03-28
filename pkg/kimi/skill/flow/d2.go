package flow

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	d2NodeIDRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
	d2BlockTagRe  = regexp.MustCompile(`^\|md$`)
	d2LineBreakRe = regexp.MustCompile(`\r\n?|\n`)
)

var d2PropertySegments = map[string]struct{}{
	"shape": {}, "style": {}, "label": {}, "link": {}, "icon": {}, "near": {}, "width": {},
	"height": {}, "direction": {}, "grid-rows": {}, "grid-columns": {}, "grid-gap": {},
	"font-size": {}, "font-family": {}, "font-color": {}, "stroke": {}, "fill": {}, "opacity": {},
	"padding": {}, "border-radius": {}, "shadow": {}, "sketch": {}, "animated": {}, "multiple": {},
	"constraint": {}, "tooltip": {},
}

// ParseD2Flowchart parses one D2 diagram into a validated Flow.
func ParseD2Flowchart(text string) (*Flow, error) {
	normalized, err := normalizeD2MarkdownBlocks(text)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]nodeDef)
	outgoing := make(map[string][]FlowEdge)

	statements, err := iterD2TopLevelStatements(normalized)
	if err != nil {
		return nil, err
	}
	for _, stmt := range statements {
		hasEdge, err := hasD2UnquotedToken(stmt.text, "->")
		if err != nil {
			return nil, lineError(stmt.lineNo, err.Error())
		}
		if hasEdge {
			if err := parseD2EdgeStatement(stmt.text, stmt.lineNo, nodes, outgoing); err != nil {
				return nil, err
			}
			continue
		}
		if err := parseD2NodeStatement(stmt.text, stmt.lineNo, nodes); err != nil {
			return nil, err
		}
	}

	flowNodes := make(map[string]FlowNode, len(nodes))
	for nodeID, def := range nodes {
		flowNodes[nodeID] = def.node
		if _, ok := outgoing[nodeID]; !ok {
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

func normalizeD2MarkdownBlocks(text string) (string, error) {
	normalized := d2LineBreakRe.ReplaceAllString(text, "\n")
	lines := strings.Split(normalized, "\n")
	outLines := make([]string, 0, len(lines))

	i := 0
	lineNo := 1
	for i < len(lines) {
		line := lines[i]
		prefix, suffix, hasColon := splitD2UnquotedOnce(line, ":")
		if !hasColon {
			outLines = append(outLines, line)
			i++
			lineNo++
			continue
		}

		suffixClean := strings.TrimSpace(stripD2UnquotedComment(suffix))
		if !d2BlockTagRe.MatchString(suffixClean) {
			outLines = append(outLines, line)
			i++
			lineNo++
			continue
		}

		startLine := lineNo
		blockLines := make([]string, 0)
		i++
		lineNo++
		for i < len(lines) {
			blockLine := lines[i]
			if strings.TrimSpace(blockLine) == "|" {
				break
			}
			blockLines = append(blockLines, blockLine)
			i++
			lineNo++
		}
		if i >= len(lines) {
			return "", lineError(startLine, "Unclosed markdown block")
		}

		dedented := dedentD2Block(blockLines)
		if len(dedented) > 0 {
			escaped := make([]string, len(dedented))
			for idx := range dedented {
				escaped[idx] = escapeD2QuotedLine(dedented[idx])
			}
			outLines = append(outLines, fmt.Sprintf(`%s: "%s`, prefix, escaped[0]))
			for idx := 1; idx < len(escaped); idx++ {
				outLines = append(outLines, escaped[idx])
			}
			outLines[len(outLines)-1] += `"`
			outLines = append(outLines, "", "")
		} else {
			outLines = append(outLines, fmt.Sprintf(`%s: ""`, prefix), "")
		}

		i++
		lineNo++
	}

	return strings.Join(outLines, "\n"), nil
}

func stripD2UnquotedComment(text string) string {
	inSingle := false
	inDouble := false
	escape := false
	for idx := 0; idx < len(text); idx++ {
		ch := text[idx]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && (inSingle || inDouble) {
			escape = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == '#' && !inSingle && !inDouble {
			return text[:idx]
		}
	}
	return text
}

func dedentD2Block(lines []string) []string {
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		stripped := strings.TrimLeft(line, " \t")
		lead := len(line) - len(stripped)
		if indent == -1 || lead < indent {
			indent = lead
		}
	}

	if indent == -1 {
		out := make([]string, len(lines))
		for i := range out {
			out[i] = ""
		}
		return out
	}

	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= indent {
			out[i] = line[indent:]
		} else {
			out[i] = ""
		}
	}
	return out
}

func escapeD2QuotedLine(line string) string {
	line = strings.ReplaceAll(line, `\\`, `\\\\`)
	return strings.ReplaceAll(line, `"`, `\\"`)
}

type d2Statement struct {
	lineNo int
	text   string
}

func iterD2TopLevelStatements(text string) ([]d2Statement, error) {
	normalized := d2LineBreakRe.ReplaceAllString(text, "\n")

	braceDepth := 0
	inSingle := false
	inDouble := false
	escape := false
	dropLine := false
	var buf strings.Builder
	lineNo := 1
	stmtLine := 1
	i := 0
	out := make([]d2Statement, 0)

	flush := func() {
		statement := strings.TrimSpace(buf.String())
		if statement != "" {
			out = append(out, d2Statement{lineNo: stmtLine, text: statement})
		}
		buf.Reset()
	}

	for i < len(normalized) {
		ch := normalized[i]
		next := byte(0)
		if i+1 < len(normalized) {
			next = normalized[i+1]
		}

		if ch == '\\' && next == '\n' {
			i += 2
			lineNo++
			continue
		}

		if ch == '\n' {
			if (inSingle || inDouble) && braceDepth == 0 && !dropLine {
				buf.WriteByte('\n')
				lineNo++
				i++
				continue
			}
			if braceDepth == 0 && !inSingle && !inDouble && !dropLine {
				flush()
			}
			buf.Reset()
			dropLine = false
			stmtLine = lineNo + 1
			lineNo++
			i++
			continue
		}

		if !inSingle && !inDouble {
			if ch == '#' {
				for i < len(normalized) && normalized[i] != '\n' {
					i++
				}
				continue
			}
			if ch == '{' {
				if braceDepth == 0 {
					flush()
					dropLine = true
					buf.Reset()
				}
				braceDepth++
				i++
				continue
			}
			if ch == '}' && braceDepth > 0 {
				braceDepth--
				i++
				continue
			}
			if ch == '}' && braceDepth == 0 {
				return nil, lineError(lineNo, "Unmatched '}'")
			}
		}

		if ch == '\'' && !inDouble && !escape {
			inSingle = !inSingle
		} else if ch == '"' && !inSingle && !escape {
			inDouble = !inDouble
		}

		if escape {
			escape = false
		} else if ch == '\\' && (inSingle || inDouble) {
			escape = true
		}

		if braceDepth == 0 && !dropLine {
			buf.WriteByte(ch)
		}
		i++
	}

	if braceDepth != 0 {
		return nil, lineError(lineNo, "Unclosed '{' block")
	}
	if inSingle || inDouble {
		return nil, lineError(lineNo, "Unclosed string")
	}

	statement := strings.TrimSpace(buf.String())
	if statement != "" {
		out = append(out, d2Statement{lineNo: stmtLine, text: statement})
	}
	return out, nil
}

func hasD2UnquotedToken(text, token string) (bool, error) {
	parts, err := splitD2OnToken(text, token)
	if err != nil {
		return false, err
	}
	return len(parts) > 1, nil
}

func parseD2EdgeStatement(statement string, lineNo int, nodes map[string]nodeDef, outgoing map[string][]FlowEdge) error {
	parts, err := splitD2OnToken(statement, "->")
	if err != nil {
		return lineError(lineNo, err.Error())
	}
	if len(parts) < 2 {
		return lineError(lineNo, "Expected edge arrow")
	}

	targetText, edgeLabel, hasLabel := splitD2UnquotedOnce(parts[len(parts)-1], ":")
	parts[len(parts)-1] = targetText

	nodeIDs := make([]string, 0, len(parts))
	for idx, part := range parts {
		nodeID, err := parseD2NodeID(part, lineNo, idx < len(parts)-1)
		if err != nil {
			return err
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	for _, nodeID := range nodeIDs {
		if isD2PropertyPath(nodeID) {
			return nil
		}
	}
	if len(nodeIDs) < 2 {
		return lineError(lineNo, "Edge must have at least two nodes")
	}

	label := ""
	if hasLabel {
		parsed, err := parseD2Label(edgeLabel, lineNo)
		if err != nil {
			return err
		}
		label = parsed
	}

	for idx := 0; idx < len(nodeIDs)-1; idx++ {
		edge := FlowEdge{Src: nodeIDs[idx], Dst: nodeIDs[idx+1]}
		if idx == len(nodeIDs)-2 {
			edge.Label = label
		}
		outgoing[edge.Src] = append(outgoing[edge.Src], edge)
		if _, ok := outgoing[edge.Dst]; !ok {
			outgoing[edge.Dst] = nil
		}
	}
	for _, nodeID := range nodeIDs {
		if _, err := addNode(nodes, nodeID, "", false, lineNo); err != nil {
			return err
		}
	}
	return nil
}

func parseD2NodeStatement(statement string, lineNo int, nodes map[string]nodeDef) error {
	nodeText, labelText, hasLabel := splitD2UnquotedOnce(statement, ":")
	if hasLabel && isD2PropertyPath(nodeText) {
		return nil
	}

	nodeID, err := parseD2NodeID(nodeText, lineNo, false)
	if err != nil {
		return err
	}

	label := ""
	explicit := false
	if hasLabel && strings.TrimSpace(labelText) == "" {
		return nil
	}
	if hasLabel {
		parsed, err := parseD2Label(labelText, lineNo)
		if err != nil {
			return err
		}
		label = parsed
		explicit = true
	}
	_, err = addNode(nodes, nodeID, label, explicit, lineNo)
	return err
}

func parseD2NodeID(text string, lineNo int, allowInlineLabel bool) (string, error) {
	cleaned := strings.TrimSpace(text)
	if allowInlineLabel {
		prefix, _, hasColon := splitD2UnquotedOnce(cleaned, ":")
		if hasColon {
			cleaned = strings.TrimSpace(prefix)
		}
	}
	if cleaned == "" {
		return "", lineError(lineNo, "Expected node id")
	}
	if !d2NodeIDRe.MatchString(cleaned) {
		return "", lineError(lineNo, fmt.Sprintf("Invalid node id %q", cleaned))
	}
	return cleaned, nil
}

func isD2PropertyPath(nodeID string) bool {
	if !strings.Contains(nodeID, ".") {
		return false
	}
	parts := strings.Split(nodeID, ".")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) < 2 {
		return false
	}
	for _, part := range clean[1:] {
		if _, ok := d2PropertySegments[part]; ok {
			return true
		}
		if strings.HasPrefix(part, "style") {
			return true
		}
	}
	_, ok := d2PropertySegments[clean[len(clean)-1]]
	return ok
}

func parseD2Label(text string, lineNo int) (string, error) {
	label := strings.TrimSpace(text)
	if label == "" {
		return "", lineError(lineNo, "Label cannot be empty")
	}
	if label[0] == '\'' || label[0] == '"' {
		return parseD2QuotedLabel(label, lineNo)
	}
	return label, nil
}

func parseD2QuotedLabel(text string, lineNo int) (string, error) {
	quote := text[0]
	var builder strings.Builder
	escape := false
	for idx := 1; idx < len(text); idx++ {
		ch := text[idx]
		if escape {
			builder.WriteByte(ch)
			escape = false
			continue
		}
		if ch == '\\' {
			escape = true
			continue
		}
		if ch == quote {
			if strings.TrimSpace(text[idx+1:]) != "" {
				return "", lineError(lineNo, "Unexpected trailing content")
			}
			return builder.String(), nil
		}
		builder.WriteByte(ch)
	}
	return "", lineError(lineNo, "Unclosed quoted label")
}

func splitD2OnToken(text, token string) ([]string, error) {
	parts := make([]string, 0, 4)
	var buf strings.Builder
	inSingle := false
	inDouble := false
	escape := false

	for idx := 0; idx < len(text); {
		if !inSingle && !inDouble && strings.HasPrefix(text[idx:], token) {
			parts = append(parts, strings.TrimSpace(buf.String()))
			buf.Reset()
			idx += len(token)
			continue
		}

		ch := text[idx]
		if escape {
			escape = false
		} else if ch == '\\' && (inSingle || inDouble) {
			escape = true
		} else if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '"' && !inSingle {
			inDouble = !inDouble
		}
		buf.WriteByte(ch)
		idx++
	}

	if inSingle || inDouble {
		return nil, fmt.Errorf("Unclosed string in statement")
	}
	parts = append(parts, strings.TrimSpace(buf.String()))
	return parts, nil
}

func splitD2UnquotedOnce(text, token string) (string, string, bool) {
	inSingle := false
	inDouble := false
	escape := false
	for idx := 0; idx < len(text); idx++ {
		ch := text[idx]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && (inSingle || inDouble) {
			escape = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if strings.HasPrefix(text[idx:], token) && !inSingle && !inDouble {
			return strings.TrimSpace(text[:idx]), strings.TrimSpace(text[idx+len(token):]), true
		}
	}
	return strings.TrimSpace(text), "", false
}
