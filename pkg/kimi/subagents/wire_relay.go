package subagents

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

type subagentWireRelay struct {
	agentID string
	parent  wire.Emitter
	writer  *SubagentOutputWriter
}

func newSubagentWireRelay(agentID string, parent wire.Emitter, writer *SubagentOutputWriter) wire.Emitter {
	return subagentWireRelay{
		agentID: strings.TrimSpace(agentID),
		parent:  parent,
		writer:  writer,
	}
}

func (r subagentWireRelay) Emit(msg wire.WireMessage) error {
	if r.writer != nil {
		r.writer.ObserveWireMessage(msg)
	}
	if r.parent == nil {
		return nil
	}

	event := wire.SubagentEvent{
		AgentID:   r.agentID,
		EventType: relayEventType(msg),
		Message:   relayEventMessage(msg),
		Payload:   relayEventPayload(msg),
	}
	if err := r.parent.Emit(event); err != nil {
		if r.writer != nil {
			r.writer.RecordError(fmt.Sprintf("subagent relay emit: %v", err))
		}
		// Relay errors should not break child execution.
		return nil
	}
	return nil
}

func relayEventType(msg wire.WireMessage) string {
	if msg == nil {
		return "unknown"
	}
	typeName := strings.TrimPrefix(fmt.Sprintf("%T", msg), "*wire.")
	typeName = strings.TrimPrefix(typeName, "wire.")
	if strings.TrimSpace(typeName) == "" {
		return "unknown"
	}
	return camelToSnake(typeName)
}

func relayEventMessage(msg wire.WireMessage) string {
	switch typed := msg.(type) {
	case wire.TextDelta:
		return strings.TrimSpace(typed.Delta)
	case wire.StepInterrupted:
		return strings.TrimSpace(typed.Reason)
	case wire.CompactionError:
		return strings.TrimSpace(typed.Error)
	case wire.Notification:
		return strings.TrimSpace(typed.Message)
	case wire.SubagentEvent:
		return strings.TrimSpace(typed.Message)
	case wire.TurnEnd:
		return strings.TrimSpace(contentPartsText(typed.Output))
	default:
		return ""
	}
}

func relayEventPayload(msg wire.WireMessage) types.JsonType {
	if msg == nil {
		return nil
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return map[string]any{
			"wire_type": relayEventType(msg),
		}
	}

	var payload any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return map[string]any{
			"wire_type": relayEventType(msg),
		}
	}
	if obj, ok := payload.(map[string]any); ok {
		obj["wire_type"] = relayEventType(msg)
		return obj
	}
	return map[string]any{
		"wire_type": relayEventType(msg),
		"value":     payload,
	}
}

func camelToSnake(value string) string {
	if value == "" {
		return ""
	}
	var out strings.Builder
	for i, r := range value {
		if unicode.IsUpper(r) {
			if i > 0 {
				out.WriteRune('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String()
}
