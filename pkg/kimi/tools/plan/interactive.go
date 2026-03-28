package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xiewanpeng/go-kimi/pkg/kimi/wire"
)

const (
	defaultQuestionTimeoutSeconds = 120
	minQuestionTimeoutSeconds     = 1
	maxQuestionTimeoutSeconds     = 600
)

var planQuestionSequence uint64

// YoloChecker reports whether yolo mode is active.
type YoloChecker func() bool

// ModeSyncer synchronizes plan mode runtime changes.
type ModeSyncer interface {
	OnPlanModeEnter(planFile string, slug string)
	OnPlanModeExit()
}

// QuestionFlow contains dependencies for interactive plan confirmations.
type QuestionFlow struct {
	Hub            *wire.Hub
	Publisher      wire.Emitter
	IsYolo         YoloChecker
	TimeoutSeconds int
}

type questionOutcome struct {
	Answer    string
	RequestID string
}

func (q QuestionFlow) enabled() bool {
	return q.Hub != nil
}

func (q QuestionFlow) isYolo() bool {
	if q.IsYolo == nil {
		return false
	}
	return q.IsYolo()
}

func (q QuestionFlow) timeout() time.Duration {
	if q.TimeoutSeconds > 0 {
		return time.Duration(q.TimeoutSeconds) * time.Second
	}
	return time.Duration(defaultQuestionTimeoutSeconds) * time.Second
}

func (q QuestionFlow) publisher() wire.Emitter {
	if q.Publisher != nil {
		return q.Publisher
	}
	return q.Hub
}

func (q QuestionFlow) askSingleChoice(
	ctx context.Context,
	requestIDPrefix string,
	prompt string,
	item wire.QuestionItem,
) (questionOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !q.enabled() {
		return questionOutcome{}, errors.New("plan question flow: wire hub is not configured")
	}
	publisher := q.publisher()
	if publisher == nil {
		return questionOutcome{}, errors.New("plan question flow: wire publisher is not configured")
	}

	requestID := newPlanQuestionID(requestIDPrefix)
	subscriber := q.Hub.Subscribe()
	defer q.Hub.Unsubscribe(subscriber)

	if err := publisher.Emit(wire.QuestionRequest{
		ID:            requestID,
		Prompt:        strings.TrimSpace(prompt),
		Items:         []wire.QuestionItem{item},
		AllowMultiple: false,
	}); err != nil {
		return questionOutcome{}, fmt.Errorf("plan question flow: publish question request: %w", err)
	}

	timeout := q.timeout()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return questionOutcome{}, fmt.Errorf(
					"plan question flow: wait response timed out after %d seconds",
					int(timeout.Seconds()),
				)
			}
			return questionOutcome{}, fmt.Errorf("plan question flow: wait response canceled: %w", waitCtx.Err())
		case message, ok := <-subscriber:
			if !ok {
				return questionOutcome{}, errors.New("plan question flow: wire subscriber closed")
			}
			response, ok := message.(wire.QuestionResponse)
			if !ok {
				continue
			}
			if strings.TrimSpace(response.RequestID) != requestID {
				continue
			}
			answer := strings.TrimSpace(response.Answers[item.ID])
			return questionOutcome{
				Answer:    strings.ToLower(answer),
				RequestID: requestID,
			}, nil
		}
	}
}

func newPlanQuestionID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "plan-question"
	}
	sequence := atomic.AddUint64(&planQuestionSequence, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), sequence)
}
