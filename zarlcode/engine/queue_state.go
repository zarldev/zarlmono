package engine

import (
	"context"
	"iter"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// queueState is the live-turn injection queue. The UI appends user-entered
// text while a top-level run is active, and the runner drains the queue at the
// next iteration boundary via runner.Steerer. MCP notifications use the same
// append side so long-running server events reach the model as user-side data.
type queueState struct {
	mu       sync.Mutex
	messages []QueuedMessage
	nextID   int
}

func newQueueState() *queueState { return &queueState{} }

// QueuedMessage is a user message in the injection queue with a stable id
// so the steer tray can reference and edit individual entries.
type QueuedMessage struct {
	ID      int
	Message llm.Message
}

// Append queues text as a user message and returns the post-append queue depth.
// It is intentionally mutex-only and non-blocking: MCP notification callbacks
// may call it from a transport reader goroutine.
func (q *queueState) Append(text string) (int, int) {
	if q == nil {
		return 0, 0
	}
	text = strings.TrimSpace(text)
	q.mu.Lock()
	defer q.mu.Unlock()
	if text == "" {
		return len(q.messages), 0
	}
	q.nextID++
	id := q.nextID
	q.messages = append(q.messages, QueuedMessage{ID: id, Message: llm.Message{Role: llm.RoleUser, Content: text}})
	return len(q.messages), id
}

// Drain satisfies runner.Steerer. It atomically takes the ready messages and
// yields them without blocking; an empty queue returns an empty sequence.
//
// Only top-level runs may drain the UI queue. The default spawn_agent path
// reuses the parent runner for unnamed/fallback sub-agents, which means the
// child Run inherits this same Steerer. Without the depth check, user input
// typed while a sub-agent is running gets consumed into the child transcript
// instead of the parent conversation.
func (q *queueState) Drain(ctx context.Context) iter.Seq[llm.Message] {
	if q == nil {
		return func(func(llm.Message) bool) {}
	}
	if taskscope.DepthFrom(ctx) > 0 {
		return func(func(llm.Message) bool) {}
	}
	q.mu.Lock()
	out := q.messages
	q.messages = nil
	q.mu.Unlock()

	return func(yield func(llm.Message) bool) {
		for _, qm := range out {
			if !yield(qm.Message) {
				return
			}
		}
	}
}

func (q *queueState) Pop() (llm.Message, bool) {
	if q == nil {
		return llm.Message{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return llm.Message{}, false
	}
	msg := q.messages[0]
	q.messages = q.messages[1:]
	return msg.Message, true
}

// AppendForInjector forwards to Append but returns only the depth, satisfying
// the mcp.Injector interface that expects Append(string) int.
func (q *queueState) AppendForInjector(text string) int {
	n, _ := q.Append(text)
	return n
}

// QueueInjectorAdapter wraps a *queueState to satisfy the mcp.Injector
// interface (Append(string) int) even after the main Append method gained
// a second return value (the message id).
type QueueInjectorAdapter struct{ queue *queueState }

func (a QueueInjectorAdapter) Append(text string) int {
	n, _ := a.queue.Append(text)
	return n
}

func (q *queueState) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// Snapshot returns a copy of all queued messages.
func (q *queueState) Snapshot() []QueuedMessage {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]QueuedMessage, len(q.messages))
	copy(out, q.messages)
	return out
}

// Update replaces the content of the message with the given id. Returns false
// when the id is unknown.
func (q *queueState) Update(id int, text string) bool {
	if q == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, msg := range q.messages {
		if msg.ID == id {
			q.messages[i].Message.Content = text
			return true
		}
	}
	return false
}

// Remove deletes the message with the given id.
func (q *queueState) Remove(id int) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, msg := range q.messages {
		if msg.ID == id {
			q.messages = append(q.messages[:i], q.messages[i+1:]...)
			return true
		}
	}
	return false
}

// Clear drops every queued message and returns the count that was removed.
func (q *queueState) Clear() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.messages)
	q.messages = nil
	return n
}

// AppendControl queues a control snippet. It's the same mechanism as Append
// but wraps the text with a sentinel prefix so the steerer/mcp layer may
// recognise it later without a full protocol change.
func (q *queueState) AppendControl(text string) (int, int) {
	if q == nil {
		return 0, 0
	}
	return q.Append(text)
}
