package teasink

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

// pumpQueue owns a bounded FIFO of values. Only previews still queued after the
// last non-preview event can be replaced; every lifecycle/content/barrier event
// fences replacement so future output cannot travel backwards across it.
type pumpQueue struct {
	mu          sync.Mutex
	slots       []tea.Msg
	head, count int
	previews    map[toolOutputKey]int
	changed     chan struct{}
	closed      bool
}

func newPumpQueue() pumpQueue {
	return pumpQueue{slots: make([]tea.Msg, pumpBuffer), previews: make(map[toolOutputKey]int), changed: make(chan struct{})}
}

func queuedPreviewKey(msg ToolOutputMsg) toolOutputKey {
	return toolOutputKeyFor(msg.TaskID, msg.Depth, msg.ExecutionID, msg.ToolID, msg.ToolName, msg.ParentExecutionID, msg.ParentToolID, msg.Sequence)
}

// tryPush never waits with the queue lock held. A failed push returns the
// current generation's wakeup channel for a cancellable space wait.
func (q *pumpQueue) tryPush(msg tea.Msg) (bool, <-chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return true, nil
	} // Shutdown intentionally discards undelivered messages.
	preview, isPreview := msg.(ToolOutputMsg)
	var key toolOutputKey
	if isPreview {
		key = queuedPreviewKey(preview)
		if slot, ok := q.previews[key]; ok {
			q.slots[slot] = msg
			return true, nil
		}
	}
	if q.count == len(q.slots) {
		return false, q.changed
	}
	slot := (q.head + q.count) % len(q.slots)
	q.slots[slot] = msg
	q.count++
	if isPreview {
		q.previews[key] = slot
	} else {
		clear(q.previews)
	}
	q.signal()
	return true, nil
}

func (q *pumpQueue) pop() (tea.Msg, <-chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		return nil, q.changed
	}
	msg := q.slots[q.head]
	if preview, ok := msg.(ToolOutputMsg); ok {
		key := queuedPreviewKey(preview)
		if slot, exists := q.previews[key]; exists && slot == q.head {
			delete(q.previews, key)
		}
	}
	q.slots[q.head] = nil
	q.head = (q.head + 1) % len(q.slots)
	q.count--
	q.signal()
	return msg, nil
}

func (q *pumpQueue) signal() {
	close(q.changed)
	q.changed = make(chan struct{})
}

func (q *pumpQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	clear(q.slots)
	clear(q.previews)
	q.count, q.head = 0, 0
	q.signal()
}
