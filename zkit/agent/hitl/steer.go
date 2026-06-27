package hitl

import (
	"context"
	"iter"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// SteerQueue is a small in-memory runner steerer for review decisions. It is
// suitable for adapters and tests; applications with an existing steer queue
// can call FormatReviewMessage and enqueue the resulting text themselves.
type SteerQueue struct {
	mu       sync.Mutex
	messages []llm.Message
}

// Append queues text as a user message. Empty text is ignored. The return value
// is the queue depth after the append.
func (q *SteerQueue) Append(text string) int {
	if q == nil {
		return 0
	}
	text = strings.TrimSpace(text)
	q.mu.Lock()
	defer q.mu.Unlock()
	if text == "" {
		return len(q.messages)
	}
	q.messages = append(q.messages, llm.Message{Role: llm.RoleUser, Content: text})
	return len(q.messages)
}

// AppendReview formats req/review and queues it as a user steer message.
func (q *SteerQueue) AppendReview(req Request, review Review) int {
	return q.Append(FormatReviewMessage(req, review))
}

// Drain satisfies runner.Steerer. It atomically drains queued review messages.
func (q *SteerQueue) Drain(context.Context) iter.Seq[llm.Message] {
	if q == nil {
		return func(func(llm.Message) bool) {}
	}
	q.mu.Lock()
	out := q.messages
	q.messages = nil
	q.mu.Unlock()
	return func(yield func(llm.Message) bool) {
		for _, msg := range out {
			if !yield(msg) {
				return
			}
		}
	}
}

// Len reports the number of queued review messages.
func (q *SteerQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}
