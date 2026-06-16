package runner

import (
	"context"
	"sync"
)

// ConversationLock is a cooperative mutex with an "active" flag — used
// by background runners that share an LLM with a real-time conversation
// pipeline. When a real-time conversation is active, the runner should
// yield (pause its tool loop) so the conversation gets the LLM's
// attention. When the conversation ends, the runner resumes.
//
// Cooperative because the runner has to *check* IsActive — nobody
// preempts it. The expected usage:
//
//	if err := lock.Wait(ctx); err != nil {
//	    return err // ctx cancelled
//	}
//	// safe to proceed
//
// Internally the lock uses sync.Cond so Release wakes Wait()ers
// immediately (no polling). Cancellation is observed via
// context.AfterFunc.
type ConversationLock struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active bool
}

// NewConversationLock creates an unlocked ConversationLock.
func NewConversationLock() *ConversationLock {
	l := &ConversationLock{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Acquire marks the conversation as active. Subsequent IsActive calls
// return true until Release is called. Idempotent — multiple Acquires
// without an intervening Release leave the lock active until the next
// Release.
func (c *ConversationLock) Acquire() {
	c.mu.Lock()
	c.active = true
	c.mu.Unlock()
}

// Release marks the conversation as no longer active and wakes any
// goroutine blocked in Wait.
func (c *ConversationLock) Release() {
	c.mu.Lock()
	c.active = false
	c.cond.Broadcast()
	c.mu.Unlock()
}

// IsActive reports whether a conversation is currently in progress.
func (c *ConversationLock) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Wait blocks until the lock becomes inactive or ctx is cancelled.
// Returns nil on a clean unlock, ctx.Err() on cancellation. No
// busy-waiting: a Release wakes the goroutine immediately, and ctx
// cancellation wakes it via context.AfterFunc.
func (c *ConversationLock) Wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return nil
	}

	// AfterFunc registers a callback that fires on ctx cancellation.
	// The callback grabs the lock and broadcasts so a Wait stuck on
	// cond.Wait wakes and observes ctx.Err. stop() removes the
	// callback on the happy path so we don't leave a goroutine
	// armed after a clean return.
	stop := context.AfterFunc(ctx, func() {
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	})
	defer stop()

	for c.active {
		c.cond.Wait()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
