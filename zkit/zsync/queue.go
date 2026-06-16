package zsync

import (
	"context"
	"io"
	"sync"
)

// Queue is a thread-safe generic FIFO queue with blocking and
// context-cancellable Pop. Implemented on a slice + sync.Cond for
// unbounded capacity, but Pop respects context cancellation by waiting
// on the context's Done channel via a per-call sentinel goroutine.
type Queue[T any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []T
	closed bool
}

var _ io.Closer = (*Queue[any])(nil)

// NewQueue creates a new thread-safe FIFO queue.
func NewQueue[T any]() *Queue[T] {
	q := &Queue[T]{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push adds an item to the back of the queue. Returns ErrQueueClosed
// after Close.
func (q *Queue[T]) Push(item T) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	q.items = append(q.items, item)
	q.cond.Signal()
	return nil
}

// Pop blocks until an item is available, then removes and returns it.
// Returns ErrQueueClosed if the queue closes while empty.
func (q *Queue[T]) Pop() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	return q.popLocked()
}

// PopContext is like Pop but returns ctx.Err() if ctx is canceled
// before an item arrives. The watcher goroutine wakes a single waiter
// (this one) by re-broadcasting; we re-check the loop condition and
// the context after each wake.
func (q *Queue[T]) PopContext(ctx context.Context) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) > 0 || q.closed {
		return q.popLocked()
	}

	// Spin up a single watcher for the lifetime of this Pop. When ctx
	// fires it broadcasts on the cond so we wake and re-check. This
	// wakes other waiters too — they re-check their own ctx and go
	// back to sleep.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-stop:
		}
	}()
	defer close(stop)

	for len(q.items) == 0 && !q.closed {
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, err
		}
		q.cond.Wait()
	}
	return q.popLocked()
}

// TryPop returns immediately. Errors with ErrQueueEmpty when no item
// is available, or ErrQueueClosed when the queue is closed and empty.
func (q *Queue[T]) TryPop() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		if q.closed {
			return zero, ErrQueueClosed
		}
		return zero, ErrQueueEmpty
	}
	return q.popLocked()
}

// popLocked assumes q.mu is held. Returns the front item or the
// closed/empty sentinel.
func (q *Queue[T]) popLocked() (T, error) {
	if len(q.items) == 0 {
		var zero T
		if q.closed {
			return zero, ErrQueueClosed
		}
		return zero, ErrQueueEmpty
	}
	item := q.items[0]
	// Zero out the slot so we don't pin the popped value's memory.
	var zero T
	q.items[0] = zero
	q.items = q.items[1:]
	return item, nil
}

// Len returns the number of items currently in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Close marks the queue closed and wakes every waiter. Push fails
// after Close; Pop drains remaining items, then errors with
// ErrQueueClosed. Idempotent.
func (q *Queue[T]) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
	return nil
}

// IsClosed reports whether Close has been called.
func (q *Queue[T]) IsClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}
