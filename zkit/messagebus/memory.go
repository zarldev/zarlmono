package messagebus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
)

// MemoryBus implements Bus using in-process subscriptions and buffered channels.
//
// It is intended for tests, local development, and single-process components
// that need the same Bus API without a network broker. Asynchronous mode delivers
// through per-subscription buffers and logs drops when a subscriber falls behind;
// synchronous mode invokes handlers inline during Publish.
type MemoryBus[T any] struct {
	mu            sync.RWMutex
	subscriptions map[string][]*memorySubscription[T]
	config        *memoryConfig
	closed        bool
}

// memoryConfig holds memory bus configuration.
type memoryConfig struct {
	bufferSize  int
	synchronous bool
}

// defaultMemoryConfig returns default configuration.
func defaultMemoryConfig() *memoryConfig {
	return &memoryConfig{
		bufferSize:  100,
		synchronous: false,
	}
}

// NewMemoryBus creates an in-memory bus.
//
// By default delivery is asynchronous with a bounded buffer per subscription.
// Pass WithSynchronous for deterministic inline delivery in tests that need to
// observe handler side effects immediately after Publish returns.
func NewMemoryBus[T any](opts ...options.Option[MemoryBus[T]]) Bus[T] {
	config := defaultMemoryConfig()

	bus := &MemoryBus[T]{
		subscriptions: make(map[string][]*memorySubscription[T]),
		config:        config,
	}

	// Apply options
	for _, opt := range opts {
		opt(bus)
	}

	return bus
}

// WithBufferSize sets the per-subscription channel capacity used in
// asynchronous delivery mode. Messages published after a subscriber's buffer is
// full are dropped and logged.
func WithBufferSize[T any](size int) options.Option[MemoryBus[T]] {
	return func(bus *MemoryBus[T]) {
		bus.config.bufferSize = size
	}
}

// WithSynchronous makes Publish call matching handlers inline before returning.
//
// This is useful for deterministic tests and local pipelines. Handler errors are
// logged and do not abort delivery to other matching subscriptions.
func WithSynchronous[T any]() options.Option[MemoryBus[T]] {
	return func(bus *MemoryBus[T]) {
		bus.config.synchronous = true
	}
}

// Publish sends data to all subscribers whose subject pattern matches subject.
func (b *MemoryBus[T]) Publish(ctx context.Context, subject string, data T) error {
	return b.PublishWithHeaders(ctx, subject, data, Headers{})
}

// PublishWithHeaders sends data and headers to all matching subscribers.
//
// Delivery discipline:
//
//  1. Subscriber snapshot is taken under bus.RLock.
//
//  2. Async path keeps the RLock for channel sends — Unsubscribe
//     needs bus.Lock to close the channel, so a held RLock guarantees
//     `sub.messages` is still open and "send on closed channel"
//     panics can't happen.
//
//  3. Sync path RELEASES the RLock before invoking handlers. A
//     synchronous handler is allowed to call Subscribe / Unsubscribe
//     / Close on the same bus; both need bus.Lock (writer). Holding
//     the RLock through the handler call would deadlock — earlier
//     shape did exactly that, and any consumer wiring up "on first
//     event, register the next subscriber" hit it.
//
//     The trade: between snapshot and invocation a concurrent
//     Unsubscribe might remove a sub. That's fine — the handler
//     closure we captured is still valid Go code; calling it after
//     the sub is "removed from the bus" just delivers one trailing
//     event. Documented as best-effort.
func (b *MemoryBus[T]) PublishWithHeaders(ctx context.Context, subject string, data T, headers Headers) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return errors.New("bus is closed")
	}

	msg := Message[T]{
		Subject:   subject,
		Data:      data,
		Headers:   headers.Clone(),
		Timestamp: time.Now(),
	}

	var matchingSubs []*memorySubscription[T]
	for pattern, subs := range b.subscriptions {
		if b.matchSubject(pattern, subject) {
			matchingSubs = append(matchingSubs, subs...)
		}
	}

	if !b.config.synchronous {
		// Async: keep RLock during channel send so Unsubscribe can't
		// close the channel under us. Default-branch drop is logged.
		for _, sub := range matchingSubs {
			select {
			case sub.messages <- msg:
			default:
				slog.WarnContext(ctx, "messagebus: dropping message — subscriber buffer full",
					"subject", subject,
					"buffer", b.config.bufferSize)
			}
		}
		b.mu.RUnlock()
		return nil
	}

	// Sync: release the lock BEFORE invoking handlers so a handler
	// that wants to Subscribe / Unsubscribe / Close can grab the
	// writer lock without deadlocking.
	b.mu.RUnlock()
	for _, sub := range matchingSubs {
		if err := sub.handler(ctx, msg); err != nil {
			slog.WarnContext(ctx, "messagebus: sync handler",
				"subject", subject,
				"error", err)
		}
	}

	return nil
}

// Subscribe registers handler for subject and returns a subscription handle.
//
// Subject matching supports exact subjects plus NATS-style `*` single-token and
// `>` remainder wildcards.
func (b *MemoryBus[T]) Subscribe(ctx context.Context, subject string, handler Handler[T]) (Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, errors.New("bus is closed")
	}

	sub := &memorySubscription[T]{
		subject: subject,
		handler: handler,
		bus:     b,
	}

	if !b.config.synchronous {
		sub.messages = make(chan Message[T], b.config.bufferSize)
		go sub.processMessages(ctx)
	}

	b.subscriptions[subject] = append(b.subscriptions[subject], sub)

	// Auto-unsubscribe when ctx cancels. Without this, a Subscribe whose
	// caller's context is cancelled without an explicit Unsubscribe leaks the
	// processMessages goroutine (async) and the map entry — the goroutine's
	// only other exit is the channel closing. Mirrors the NATS bus; fires once
	// and is a safe no-op when Unsubscribe was already called (idempotent).
	context.AfterFunc(ctx, func() { _ = sub.Unsubscribe() })

	return sub, nil
}

// QueueSubscribe registers handler for subject using the Bus queue-subscribe API.
//
// The in-memory implementation does not coordinate queue groups; it currently
// behaves like Subscribe and delivers to each matching subscription.
func (b *MemoryBus[T]) QueueSubscribe(
	ctx context.Context,
	subject string,
	queue string,
	handler Handler[T],
) (Subscription, error) {
	// For memory implementation, queue groups are simulated by round-robin delivery
	return b.Subscribe(ctx, subject, handler)
}

// Request publishes data with a temporary reply subject and waits for one reply.
func (b *MemoryBus[T]) Request(ctx context.Context, subject string, data T, timeout time.Duration) (T, error) {
	var zero T

	// Create reply subject
	replySubject := fmt.Sprintf("_REPLY.%d", time.Now().UnixNano())

	// Set up reply subscription
	replyChan := make(chan T, 1)
	errorChan := make(chan error, 1)

	replyHandler := func(ctx context.Context, msg Message[T]) error {
		select {
		case replyChan <- msg.Data:
		default:
		}
		return nil
	}

	sub, err := b.Subscribe(ctx, replySubject, replyHandler)
	if err != nil {
		return zero, fmt.Errorf("create reply subscription: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// Add reply subject to headers
	headers := Headers{"reply-to": replySubject}

	// Publish request
	if err := b.PublishWithHeaders(ctx, subject, data, headers); err != nil {
		return zero, fmt.Errorf("publish request: %w", err)
	}

	// Wait for reply with timeout
	select {
	case reply := <-replyChan:
		return reply, nil
	case err := <-errorChan:
		return zero, err
	case <-time.After(timeout):
		return zero, errors.New("request timeout")
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// Close marks the bus closed (subsequent Publish/Subscribe return errors),
// closes every async subscription channel under the bus lock — buffered
// messages drain to handlers before each processMessages goroutine exits —
// and empties the subscription map, so later Close or Unsubscribe calls
// are safe no-ops.
func (b *MemoryBus[T]) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true

	// Close all subscriptions. We hold bus.Lock, so no publisher
	// can be mid-iterate; safe to close channels directly. The
	// valid flag is gone — closed channel + missing from b.subscriptions
	// is the new "invalid" signal. Don't nil out s.messages: see
	// Unsubscribe's matching comment.
	for _, subs := range b.subscriptions {
		for _, sub := range subs {
			if sub.messages != nil {
				close(sub.messages)
			}
		}
	}

	b.subscriptions = make(map[string][]*memorySubscription[T])

	return nil
}

// Helper methods.
func (b *MemoryBus[T]) matchSubject(pattern, subject string) bool {
	// Simple pattern matching for NATS-like subjects
	// Supports wildcards: * (single token), > (multiple tokens)

	if pattern == subject {
		return true
	}

	patternParts := strings.Split(pattern, ".")
	subjectParts := strings.Split(subject, ".")

	return b.matchParts(patternParts, subjectParts)
}

func (b *MemoryBus[T]) matchParts(pattern, subject []string) bool {
	if len(pattern) == 0 {
		return len(subject) == 0
	}

	if pattern[0] == ">" {
		return true // > matches everything remaining
	}

	if len(subject) == 0 {
		return false
	}

	if pattern[0] == "*" || pattern[0] == subject[0] {
		return b.matchParts(pattern[1:], subject[1:])
	}

	return false
}

// memorySubscription represents an in-memory subscription.
//
// # Lifecycle / concurrency
//
// The bus lock (MemoryBus.mu) owns subscription lifecycle. Publishers
// hold bus.RLock while iterating subscribers and sending; Unsubscribe
// must acquire bus.Lock (writer) so it cannot run concurrent with a
// publisher mid-send. That's how we close the channel safely:
// Unsubscribe holds bus.Lock, removes the sub from b.subscriptions,
// THEN closes — by which point no publisher could still be holding a
// snapshot of this sub. Earlier the order was reversed (per-sub mutex
// closes channel BEFORE acquiring bus lock) which left a window
// where a still-iterating publisher could send on a closed channel
// and panic.
//
// The `valid` flag is gone: bus membership IS the validity check.
// processMessages exits via the natural `range channel-closed` path,
// which fires the moment Unsubscribe closes the channel under bus.Lock.
type memorySubscription[T any] struct {
	subject  string
	handler  Handler[T]
	messages chan Message[T]
	bus      *MemoryBus[T]
}

func (s *memorySubscription[T]) processMessages(ctx context.Context) {
	// Natural exit on close — Unsubscribe closes s.messages under
	// the bus lock, after removing this sub from b.subscriptions,
	// so no publisher will ever send again after the close.
	for msg := range s.messages {
		if err := s.handler(ctx, msg); err != nil {
			slog.WarnContext(ctx, "messagebus: async handler",
				"subject", s.subject,
				"error", err)
		}
	}
}

func (s *memorySubscription[T]) Unsubscribe() error {
	// Bus lock FIRST. Holding the writer lock waits out any
	// in-flight publishers (they hold bus.RLock). After the lock
	// acquires, no publisher can be mid-iterate or mid-send for
	// any subscription on this bus — safe to close the channel.
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()

	// found tracks whether THIS call is the one removing the sub from the bus.
	// Only the remover closes the channel — so a second Unsubscribe, or an
	// Unsubscribe after Close (which already closed every channel and emptied
	// the map), is a safe no-op instead of a "close of closed channel" panic.
	found := false
	subs := s.bus.subscriptions[s.subject]
	for i, sub := range subs {
		if sub == s {
			s.bus.subscriptions[s.subject] = append(subs[:i], subs[i+1:]...)
			found = true
			break
		}
	}
	if len(s.bus.subscriptions[s.subject]) == 0 {
		delete(s.bus.subscriptions, s.subject)
	}

	// Close inside the bus lock so the close synchronises with the
	// publisher's last bus-RLock release. processMessages will see
	// the closed channel and exit via natural range termination;
	// any messages already buffered drain to the handler before the
	// loop returns. We DON'T nil out s.messages — processMessages
	// captured the channel value at loop entry, so a nil-write
	// here would be functionally fine but races against the
	// goroutine's unsynchronised read at loop start. Closing is
	// the signal that matters; the GC reclaims the channel after
	// the goroutine exits. Only close when we're the remover (see found).
	if found && s.messages != nil {
		close(s.messages)
	}
	return nil
}

// IsValid reports whether this subscription is still attached to
// the bus. Now derived from bus membership rather than a separate
// flag, so there's only one source of truth and no race between
// "we say valid" and "we're actually wired up.".
func (s *memorySubscription[T]) IsValid() bool {
	s.bus.mu.RLock()
	defer s.bus.mu.RUnlock()
	return slices.Contains(s.bus.subscriptions[s.subject], s)
}
