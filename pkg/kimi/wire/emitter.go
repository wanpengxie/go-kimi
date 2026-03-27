package wire

import "errors"

// Emitter emits one wire message to an external sink.
type Emitter interface {
	Emit(msg WireMessage) error
}

// ChannelEmitter writes wire messages into a channel.
type ChannelEmitter struct {
	Ch chan<- WireMessage
}

// Emit sends one message into the configured channel.
func (e ChannelEmitter) Emit(msg WireMessage) error {
	if e.Ch == nil {
		return errors.New("wire emitter: nil channel")
	}
	if isNilWireMessage(msg) {
		return errors.New("wire emitter: nil message")
	}

	e.Ch <- msg
	return nil
}

// NoopEmitter drops all emitted messages.
type NoopEmitter struct{}

// Emit implements Emitter.
func (NoopEmitter) Emit(_ WireMessage) error {
	return nil
}
