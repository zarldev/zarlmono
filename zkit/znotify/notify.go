package znotify

import (
	"context"
	"slices"
	"sync"

	"github.com/zarldev/zarlmono/zkit/options"
)

// defaultSubscriberBuffer is the channel buffer size used when
// WithSubscriberBuffer isn't supplied. Small enough to back-pressure
// genuinely slow consumers (which silently drop) without flooding
// memory; consumers that need deeper queues drain into their own
// structure.
const defaultSubscriberBuffer = 16

// defaultPendingPerSession caps the per-session offline queue. A
// session that never reconnects would otherwise grow its pending
// list without bound — the original shape kept appending to
// s.pending[sid] for the lifetime of the process. Pushes past the
// cap rotate the queue: drop the oldest entry so the newest still
// lands. Use [WithPendingPerSession] to override.
//
// The package's contract is "best-effort live delivery, with offline
// queueing as the durability layer for sessions you actually care
// about" (see AGENTS.md). A bounded queue is consistent with that —
// consumers needing guaranteed retention persist the data themselves.
const defaultPendingPerSession = 1024

// Notification is a message routed to one (or all) active sessions —
// tool execution updates, lifecycle events, findings, etc. Pure data;
// transport is NotificationStore's job.
type Notification struct {
	// SessionID is the routing key. Empty means "no specific session"
	// (treated like a broadcast on the queue side).
	SessionID string

	// ToolName, when non-empty, identifies which tool produced the
	// notification (used by UIs to attach the message to a tool's
	// progress panel).
	ToolName string

	// Content is the human- or LLM-facing payload. Format is the
	// caller's call: plain text, JSON, markdown.
	Content string

	// Broadcast marks a notification as "any active frontend should
	// see this" — set for lifecycle events (task complete, findings
	// available, gesture cue) that originate from a long-lived
	// session the user may no longer have open. Push fans these out
	// to every active subscriber AND queues a copy on the originating
	// session so its next Drain sees it.
	Broadcast bool
}

// NotificationStore is a session-keyed pub/sub with offline delivery.
// Live subscribers receive notifications immediately; sessions with no
// active subscriber accumulate notifications in a per-session queue
// that Drain returns on demand. Broadcast notifications fan out to
// every active subscriber regardless of session.
//
// # Concurrency
//
// All methods are safe for concurrent calls — internal state is
// guarded by a single mutex. Producers (Push, Broadcast) and
// lifecycle calls (Subscribe, Unsubscribe, Drain) serialise through
// it, which keeps the per-channel close discipline correct: any
// channel returned by Subscribe is closed by Unsubscribe under the
// same lock that Push/Broadcast use to send, so the close cannot
// race a send.
//
// External code MUST NOT send to a channel returned by Subscribe;
// the store owns the producer side. Doing so risks panicking on
// send-to-closed when Unsubscribe runs.
type NotificationStore struct {
	mu                sync.Mutex
	pending           map[string][]Notification
	subscribers       map[string][]chan Notification
	subscriberBuf     int
	pendingPerSession int
}

// NewNotificationStore creates an empty store. Apply
// WithSubscriberBuffer to override the default channel buffer (16),
// or [WithPendingPerSession] to change the offline-queue cap.
func NewNotificationStore(opts ...options.Option[NotificationStore]) *NotificationStore {
	s := &NotificationStore{
		pending:           make(map[string][]Notification),
		subscribers:       make(map[string][]chan Notification),
		subscriberBuf:     defaultSubscriberBuffer,
		pendingPerSession: defaultPendingPerSession,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithPendingPerSession overrides the per-session offline queue
// cap. Pushes past the cap drop the oldest entry; the newest always
// lands. Pass <=0 to leave the default. There's no "unlimited" mode
// — memory safety is the whole point of the cap.
func WithPendingPerSession(n int) options.Option[NotificationStore] {
	return func(s *NotificationStore) {
		if n > 0 {
			s.pendingPerSession = n
		}
	}
}

// appendPending appends n to the session's offline queue, rotating
// off the oldest entry when the queue is at cap. Centralised so
// every Push / Broadcast path applies the same bound.
func (s *NotificationStore) appendPending(sid string, n Notification) {
	cur := s.pending[sid]
	if s.pendingPerSession > 0 && len(cur) >= s.pendingPerSession {
		// Drop the oldest entry so the newest is recorded. Shift in
		// place (copy is cheap relative to the alternative of letting
		// the queue grow unbounded).
		cur = append(cur[:0], cur[1:]...)
	}
	s.pending[sid] = append(cur, n)
}

// WithSubscriberBuffer sets the buffer size of channels returned by
// Subscribe. Larger values absorb bigger bursts before drops kick in;
// smaller values back-pressure slow consumers sooner. Values <= 0
// are ignored.
func WithSubscriberBuffer(n int) options.Option[NotificationStore] {
	return func(s *NotificationStore) {
		if n > 0 {
			s.subscriberBuf = n
		}
	}
}

// Push delivers a notification. With a live subscriber for the
// session, the notification is sent immediately (drops if the
// subscriber's buffer is full). With no live subscriber, the
// notification is queued for Drain.
//
// Broadcast notifications are fanned out to every active subscriber
// so lifecycle events from a stale session still reach the active
// frontend. The originating session also gets a queued copy so its
// next Drain reflects the event.
func (s *NotificationStore) Push(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n.Broadcast {
		for sid, subs := range s.subscribers {
			if sid == "" {
				continue
			}
			c := n
			c.SessionID = sid
			for _, ch := range subs {
				select {
				case ch <- c:
				default:
				}
			}
		}
		// Queue on the originating session so its next Drain sees it.
		if n.SessionID != "" {
			s.appendPending(n.SessionID, n)
		}
		return
	}

	subs := s.subscribers[n.SessionID]
	if len(subs) > 0 {
		for _, ch := range subs {
			select {
			case ch <- n:
			default:
			}
		}
		return
	}

	s.appendPending(n.SessionID, n)
}

// Broadcast delivers a notification to every active subscriber AND
// queues a copy on every known session's pending list so the next
// Drain picks it up as context. Use for system-wide events like "tool
// X was just installed" — anything every session needs to see whether
// or not it has a live subscriber.
func (s *NotificationStore) Broadcast(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reached := make(map[string]bool)
	for sid, subs := range s.subscribers {
		if sid == "" {
			continue
		}
		c := n
		c.SessionID = sid
		for _, ch := range subs {
			select {
			case ch <- c:
			default:
			}
		}
		s.appendPending(sid, c)
		reached[sid] = true
	}
	// Also queue on any session with existing pending work so its
	// next Drain sees the broadcast.
	for sid := range s.pending {
		if sid == "" || reached[sid] {
			continue
		}
		c := n
		c.SessionID = sid
		s.appendPending(sid, c)
	}
}

// Drain returns and clears all pending notifications for a session.
func (s *NotificationStore) Drain(sessionID string) []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.pending[sessionID]
	delete(s.pending, sessionID)
	return ns
}

// Subscribe returns a channel that receives notifications for the
// session. The returned channel has the configured buffer size; slow
// readers silently drop pushed messages.
//
// If ctx is not nil, the subscription auto-unsubscribes (and the
// channel closes) when ctx is done. Callers that prefer explicit
// lifecycle pass context.Background() and call Unsubscribe themselves.
func (s *NotificationStore) Subscribe(ctx context.Context, sessionID string) <-chan Notification {
	ch := make(chan Notification, s.subscriberBuf)
	s.mu.Lock()
	s.subscribers[sessionID] = append(s.subscribers[sessionID], ch)
	s.mu.Unlock()
	if ctx != nil {
		context.AfterFunc(ctx, func() { s.unsubscribe(sessionID, ch) })
	}
	return ch
}

// Unsubscribe removes a subscription channel and closes it. Pass the
// channel returned by Subscribe — the read-only type means callers
// must hold the original ref or re-subscribe.
//
// Evicts the session from pending cache if this was the last
// subscriber for that session, preventing stale accumulation.
func (s *NotificationStore) Unsubscribe(sessionID string, ch <-chan Notification) {
	s.unsubscribe(sessionID, ch)
}

// unsubscribe is the internal removal path. Idempotent — calling it
// twice on the same channel is safe (the second call finds the
// channel already gone and returns).
func (s *NotificationStore) unsubscribe(sessionID string, ch <-chan Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subscribers[sessionID]
	// We need to compare against the original send-capable chan
	// type — search by identity using slices.IndexFunc.
	i := slices.IndexFunc(subs, func(c chan Notification) bool { return c == ch })
	if i < 0 {
		return
	}
	target := subs[i]
	s.subscribers[sessionID] = slices.Delete(subs, i, i+1)
	if len(s.subscribers[sessionID]) == 0 {
		delete(s.subscribers, sessionID)
	}
	// Evict this session's pending notifications when the last
	// subscriber unsubscribes, preventing stale accumulation.
	if _, ok := s.pending[sessionID]; ok && len(s.subscribers[sessionID]) == 0 {
		delete(s.pending, sessionID)
	}
	close(target)
}
