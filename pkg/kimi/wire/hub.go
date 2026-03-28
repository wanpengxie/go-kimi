package wire

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/types"
)

const (
	defaultHubSubscriberBuffer   = 32
	defaultMergedOutputBufferLen = 64
)

// Hub is a single-producer multi-consumer wire broadcaster.
//
// Publish is non-blocking per subscriber. When one subscriber channel is full,
// the current message is dropped only for that subscriber.
type Hub struct {
	mu               sync.RWMutex
	subscribers      map[<-chan WireMessage]chan WireMessage
	subscriberBuffer int
	closed           bool
}

// NewHub creates one hub with subscriber buffer size.
func NewHub(subscriberBuffer int) *Hub {
	if subscriberBuffer <= 0 {
		subscriberBuffer = defaultHubSubscriberBuffer
	}
	return &Hub{
		subscribers:      map[<-chan WireMessage]chan WireMessage{},
		subscriberBuffer: subscriberBuffer,
	}
}

// Subscribe registers one subscriber and returns its receive-only channel.
func (h *Hub) Subscribe() <-chan WireMessage {
	if h == nil {
		ch := make(chan WireMessage)
		close(ch)
		return ch
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan WireMessage, h.subscriberBuffer)
	readOnly := (<-chan WireMessage)(ch)
	if h.closed {
		close(ch)
		return readOnly
	}
	h.subscribers[readOnly] = ch
	return readOnly
}

// Unsubscribe removes one subscriber and closes its channel.
func (h *Hub) Unsubscribe(ch <-chan WireMessage) {
	if h == nil || ch == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	subscriber, ok := h.subscribers[ch]
	if !ok {
		return
	}
	delete(h.subscribers, ch)
	close(subscriber)
}

// Publish broadcasts one message to all subscribers without blocking.
func (h *Hub) Publish(msg WireMessage) {
	if h == nil || isNilWireMessage(msg) {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || len(h.subscribers) == 0 {
		return
	}
	for _, subscriber := range h.subscribers {
		select {
		case subscriber <- msg:
		default:
		}
	}
}

// Emit implements Emitter by publishing into the hub.
func (h *Hub) Emit(msg WireMessage) error {
	if h == nil {
		return errors.New("wire hub: nil")
	}
	if isNilWireMessage(msg) {
		return errors.New("wire hub: nil message")
	}
	h.Publish(msg)
	return nil
}

// Close closes all subscriber channels and rejects future subscriptions.
func (h *Hub) Close() {
	if h == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}
	h.closed = true
	for key, subscriber := range h.subscribers {
		delete(h.subscribers, key)
		close(subscriber)
	}
}

// MergingSubscriber subscribes to one hub and merges text deltas into turn_end output.
type MergingSubscriber struct {
	hub    *Hub
	input  <-chan WireMessage
	output chan WireMessage

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewMergingSubscriber creates one merging subscriber and starts its consume loop.
func NewMergingSubscriber(hub *Hub, outputBuffer int) *MergingSubscriber {
	if outputBuffer <= 0 {
		outputBuffer = defaultMergedOutputBufferLen
	}

	subscriber := &MergingSubscriber{
		hub:    hub,
		output: make(chan WireMessage, outputBuffer),
		done:   make(chan struct{}),
	}
	if hub != nil {
		subscriber.input = hub.Subscribe()
	}

	subscriber.wg.Add(1)
	go subscriber.run()
	return subscriber
}

// Messages returns merged output stream.
func (s *MergingSubscriber) Messages() <-chan WireMessage {
	if s == nil {
		ch := make(chan WireMessage)
		close(ch)
		return ch
	}
	return s.output
}

// Close stops consuming and unsubscribes from hub.
func (s *MergingSubscriber) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.hub != nil && s.input != nil {
			s.hub.Unsubscribe(s.input)
		}
		close(s.done)
		s.wg.Wait()
	})
}

func (s *MergingSubscriber) run() {
	defer s.wg.Done()
	defer close(s.output)

	if s.input == nil {
		return
	}

	turnDeltas := map[string]string{}
	for {
		select {
		case <-s.done:
			return
		case message, ok := <-s.input:
			if !ok {
				return
			}
			if isNilWireMessage(message) {
				continue
			}
			if !s.handleMessage(message, turnDeltas) {
				return
			}
		}
	}
}

func (s *MergingSubscriber) handleMessage(message WireMessage, turnDeltas map[string]string) bool {
	switch typed := message.(type) {
	case TextDelta:
		turnID := strings.TrimSpace(typed.TurnID)
		if turnID == "" {
			return s.emit(message)
		}
		turnDeltas[turnID] = turnDeltas[turnID] + typed.Delta
		return true
	case TurnBegin:
		turnID := strings.TrimSpace(typed.TurnID)
		if turnID != "" {
			delete(turnDeltas, turnID)
		}
		return s.emit(message)
	case TurnEnd:
		turnID := strings.TrimSpace(typed.TurnID)
		if turnID != "" {
			merged := strings.TrimSpace(turnDeltas[turnID])
			if merged != "" {
				typed.Output = mergeTurnOutput(typed.Output, merged)
			}
			delete(turnDeltas, turnID)
		}
		return s.emit(typed)
	default:
		return s.emit(message)
	}
}

func (s *MergingSubscriber) emit(message WireMessage) bool {
	select {
	case <-s.done:
		return false
	case s.output <- message:
		return true
	}
}

func mergeTurnOutput(output types.ContentParts, mergedText string) types.ContentParts {
	mergedText = strings.TrimSpace(mergedText)
	if mergedText == "" {
		return output
	}

	if hasTextPart(output) {
		return output
	}

	merged := make(types.ContentParts, len(output), len(output)+1)
	copy(merged, output)
	merged = append(merged, types.TextPart{Text: mergedText})
	return merged
}

func hasTextPart(parts types.ContentParts) bool {
	for i := range parts {
		switch typed := parts[i].(type) {
		case types.TextPart:
			if strings.TrimSpace(typed.Text) != "" {
				return true
			}
		case *types.TextPart:
			if typed != nil && strings.TrimSpace(typed.Text) != "" {
				return true
			}
		}
	}
	return false
}

// Recorder persists wire messages from one merged stream into a WireFile.
type Recorder struct {
	file   *WireFile
	source <-chan WireMessage

	closeOnce sync.Once
	done      chan struct{}
	live      atomic.Bool
	wg        sync.WaitGroup

	mu  sync.Mutex
	err error
}

// NewRecorder creates and starts one recorder.
func NewRecorder(file *WireFile, source <-chan WireMessage) *Recorder {
	recorder := &Recorder{
		file:   file,
		source: source,
		done:   make(chan struct{}),
	}
	if file == nil {
		recorder.err = errors.New("wire recorder: nil wire file")
		return recorder
	}
	if source == nil {
		recorder.err = errors.New("wire recorder: nil source")
		return recorder
	}

	recorder.live.Store(true)
	recorder.wg.Add(1)
	go recorder.run()
	return recorder
}

// Close waits until recorder loop exits and returns terminal recorder error.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		close(r.done)
		if r.live.Load() {
			r.wg.Wait()
		}
	})
	return r.Err()
}

// Err returns recorder terminal error, if any.
func (r *Recorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *Recorder) run() {
	defer r.live.Store(false)
	defer r.wg.Done()

	for {
		select {
		case <-r.done:
			drainLimit := len(r.source)
			for i := 0; i < drainLimit; i++ {
				select {
				case message, ok := <-r.source:
					if !ok {
						return
					}
					if !r.appendMessage(message) {
						return
					}
				default:
					return
				}
			}
			return
		case message, ok := <-r.source:
			if !ok {
				return
			}
			if !r.appendMessage(message) {
				return
			}
		}
	}
}

func (r *Recorder) appendMessage(message WireMessage) bool {
	if isNilWireMessage(message) {
		return true
	}
	if err := r.file.AppendMessage(message); err != nil {
		r.setErr(err)
		return false
	}
	return true
}

func (r *Recorder) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}
