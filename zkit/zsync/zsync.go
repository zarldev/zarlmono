// Package zsync provides thread-safe generic data structures (Map,
// Set, Queue) on top of sync. The package name carries the "z"
// prefix; the types do not — i.e. zsync.Map, not zsync.ZMap.
//
// # Types
//
// Map: thread-safe generic map with read-write locking.
//
// Set: thread-safe generic set, implemented over Map[T, struct{}].
//
// Queue: thread-safe generic FIFO queue with blocking Pop, context-
// cancellable PopContext, and graceful Close.
//
// # Errors
//
//   - ErrNotFound: returned by Map.Get when the key is missing
//   - ErrQueueClosed: returned by Queue when the queue is closed
//   - ErrQueueEmpty: returned by Queue.TryPop when the queue is empty
//
// PopContext returns ctx.Err() (Canceled or DeadlineExceeded) on
// cancellation rather than a package sentinel — callers can compare
// with errors.Is(ctx.Err()) directly.
package zsync

import "errors"

var (
	// ErrNotFound reports a lookup for a key the map doesn't hold.
	ErrNotFound = errors.New("key not found")
	// ErrQueueClosed reports an operation on a queue after Close —
	// producers and consumers both see it once shutdown begins.
	ErrQueueClosed = errors.New("queue closed")
	// ErrQueueEmpty reports a non-blocking take from an empty queue.
	ErrQueueEmpty = errors.New("queue empty")
)
