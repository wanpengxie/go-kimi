package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// WireMessageEnvelope wraps a polymorphic wire message with its discriminator.
type WireMessageEnvelope struct {
	Type    WireMessageType `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type wireMessageDecoder func(payload json.RawMessage) (WireMessage, error)

var wireMessageDecoders = map[WireMessageType]wireMessageDecoder{
	WireMessageTypeTurnBegin:       decodeWireMessage[TurnBegin],
	WireMessageTypeSteerInput:      decodeWireMessage[SteerInput],
	WireMessageTypeTurnEnd:         decodeWireMessage[TurnEnd],
	WireMessageTypeStepBegin:       decodeWireMessage[StepBegin],
	WireMessageTypeStepInterrupted: decodeWireMessage[StepInterrupted],
	WireMessageTypeCompactionBegin: decodeWireMessage[CompactionBegin],
	WireMessageTypeCompactionEnd:   decodeWireMessage[CompactionEnd],
	WireMessageTypeMCPLoadingBegin: decodeWireMessage[MCPLoadingBegin],
	WireMessageTypeMCPLoadingEnd:   decodeWireMessage[MCPLoadingEnd],
	WireMessageTypeStatusUpdate:    decodeWireMessage[StatusUpdate],
	WireMessageTypeNotification:    decodeWireMessage[Notification],
	WireMessageTypeSubagentEvent:   decodeWireMessage[SubagentEvent],

	WireMessageTypeApprovalRequest:  decodeWireMessage[ApprovalRequest],
	WireMessageTypeApprovalResponse: decodeWireMessage[ApprovalResponse],
	WireMessageTypeToolCallRequest:  decodeWireMessage[ToolCallRequest],
	WireMessageTypeQuestionRequest:  decodeWireMessage[QuestionRequest],
	WireMessageTypeQuestionResponse: decodeWireMessage[QuestionResponse],
	WireMessageTypeQuestionOption:   decodeWireMessage[QuestionOption],
	WireMessageTypeQuestionItem:     decodeWireMessage[QuestionItem],
}

// SerializeWireMessage wraps a message into a type-tagged envelope.
func SerializeWireMessage(msg WireMessage) (WireMessageEnvelope, error) {
	if isNilWireMessage(msg) {
		return WireMessageEnvelope{}, errors.New("wire message: nil")
	}

	messageType := msg.wireMessageType()
	if messageType == "" {
		return WireMessageEnvelope{}, errors.New("wire message: missing type")
	}
	if _, ok := wireMessageDecoders[messageType]; !ok {
		return WireMessageEnvelope{}, fmt.Errorf("wire message: unregistered type %q", messageType)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return WireMessageEnvelope{}, fmt.Errorf("wire message: marshal %q: %w", messageType, err)
	}

	return WireMessageEnvelope{
		Type:    messageType,
		Payload: payload,
	}, nil
}

// DeserializeWireMessage unwraps a polymorphic envelope represented as map data.
func DeserializeWireMessage(data map[string]any) (WireMessage, error) {
	if data == nil {
		return nil, errors.New("wire message envelope: nil data")
	}

	rawEnvelope, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("wire message envelope: marshal map: %w", err)
	}

	var envelope WireMessageEnvelope
	if err := json.Unmarshal(rawEnvelope, &envelope); err != nil {
		return nil, fmt.Errorf("wire message envelope: decode: %w", err)
	}

	return DeserializeWireMessageEnvelope(envelope)
}

// DeserializeWireMessageEnvelope unwraps a message envelope into a concrete message.
func DeserializeWireMessageEnvelope(envelope WireMessageEnvelope) (WireMessage, error) {
	if envelope.Type == "" {
		return nil, errors.New("wire message envelope: missing type")
	}
	decoder, ok := wireMessageDecoders[envelope.Type]
	if !ok {
		return nil, fmt.Errorf("wire message envelope: unknown type %q", envelope.Type)
	}
	if len(envelope.Payload) == 0 {
		return nil, fmt.Errorf("wire message envelope: missing payload for type %q", envelope.Type)
	}

	msg, err := decoder(envelope.Payload)
	if err != nil {
		return nil, fmt.Errorf("wire message envelope: unmarshal %q: %w", envelope.Type, err)
	}
	return msg, nil
}

func decodeWireMessage[T WireMessage](payload json.RawMessage) (WireMessage, error) {
	var message T
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	return message, nil
}

func isNilWireMessage(msg WireMessage) bool {
	if msg == nil {
		return true
	}
	value := reflect.ValueOf(msg)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
