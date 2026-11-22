package teasink

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// ErrApplicationsUnavailable means this sink does not provide application receipts.
var ErrApplicationsUnavailable = errors.New("application receipts unavailable")

// ErrApplicationDeliveryLost means application can no longer be proved. Closing
// the sink or panicking during application makes its outstanding outcome unknown.
var ErrApplicationDeliveryLost = errors.New("application delivery lost")

// ApplicationDelivery is an opaque, single-use claim on an owned FIFO entry.
// It contains no payload; stale and duplicate deliveries cannot apply an event.
type ApplicationDelivery struct {
	owner           *Sink
	sequence, epoch uint64
}

// NewApplicationSink constructs an opt-in application-acknowledged sink. Unlike
// New(nil), it retains accepted events before SetSend wires the consumer. Exactly
// one forwarded, unacknowledged event occupies the head of the bounded FIFO.
// The caller owns Close, after all producers and applications have joined.
func NewApplicationSink(send func(tea.Msg)) *Sink {
	s := New(nil)
	s.queue.application = true
	s.SetSend(send)
	return s
}

// Apply claims the event atomically and calls apply without sink or queue locks.
// Normal return acknowledges and releases the entry, including during takeover.
// A panic is preserved and marks the outcome lost: partial application is never
// retried. False means this token is stale, duplicated, closed or already claimed.
func (d ApplicationDelivery) Apply(apply func(tea.Msg)) bool {
	if d.owner == nil {
		return false
	}
	q := &d.owner.queue
	q.mu.Lock()
	if q.closed || q.lost || q.claimed || q.count == 0 || d.sequence != q.sequence || d.epoch != q.epoch {
		q.mu.Unlock()
		return false
	}
	q.claimed = true
	msg := q.slots[q.head]
	q.mu.Unlock()
	completed := false
	defer func() {
		q.mu.Lock()
		q.claimed = false
		if completed && !q.closed {
			q.removeHead()
		} else {
			q.lost = true
			q.signal()
		}
		q.mu.Unlock()
	}()
	apply(msg)
	completed = true
	return true
}

// TakeApplications permanently transfers consumption to NextApplication. It
// revokes unclaimed transport tokens and waits for the current send and claimed
// callback, without closing the FIFO to producers. On timeout the same owner,
// payload and join state remain available for retry. Plain or closed/lost sinks
// return a stable error rather than claiming application proof.
func (s *Sink) TakeApplications(ctx context.Context) error {
	q := &s.queue
	q.mu.Lock()
	if !q.application {
		q.mu.Unlock()
		return ErrApplicationsUnavailable
	}
	if !q.taken {
		q.taken = true
		q.epoch++
		q.signal()
	}
	for {
		if q.closed || q.lost {
			q.mu.Unlock()
			return ErrApplicationDeliveryLost
		}
		if !q.sending && !q.claimed {
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		q.mu.Lock()
	}
}

// NextApplication returns the oldest unapplied entry after takeover. The changed
// channel is captured atomically with the empty/busy observation and can be
// selected with producer completion and cancellation. Call TakeApplications to
// check for unavailable or lost proof; a closed sink never yields deliveries.
func (s *Sink) NextApplication() (ApplicationDelivery, bool, <-chan struct{}) {
	q := &s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.application && q.taken && !q.closed && !q.lost && !q.claimed && !q.sending && q.count != 0 {
		if barrier, ok := q.slots[q.head].(barrierMsg); ok {
			close(barrier.ack)
			q.removeHead()
			continue
		}
		q.freezeHeadPreview()
		return ApplicationDelivery{owner: s, sequence: q.sequence, epoch: q.epoch}, true, q.changed
	}
	return ApplicationDelivery{}, false, q.changed
}

func (q *pumpQueue) freezeHeadPreview() {
	if preview, ok := q.slots[q.head].(ToolOutputMsg); ok {
		key := queuedPreviewKey(preview)
		if slot, exists := q.previews[key]; exists && slot == q.head {
			delete(q.previews, key)
		}
	}
}

func (s *Sink) pumpApplications() {
	q := &s.queue
	for {
		q.mu.Lock()
		if q.closed || q.taken || q.lost {
			q.mu.Unlock()
			return
		}
		send := s.send.Load()
		if q.count == 0 || q.forwarded || send == nil {
			changed := q.changed
			q.mu.Unlock()
			select {
			case <-s.stop:
				return
			case <-changed:
			}
			continue
		}
		if barrier, ok := q.slots[q.head].(barrierMsg); ok {
			close(barrier.ack)
			q.removeHead()
			q.mu.Unlock()
			continue
		}
		q.freezeHeadPreview()
		q.forwarded, q.sending = true, true
		delivery := ApplicationDelivery{owner: s, sequence: q.sequence, epoch: q.epoch}
		q.mu.Unlock()
		s.sendApplication(send, delivery)
	}
}

func (s *Sink) sendApplication(send *sendFunc, delivery ApplicationDelivery) {
	defer func() {
		failure := recover()
		q := &s.queue
		q.mu.Lock()
		q.sending = false
		if failure != nil {
			q.lost = true
		}
		q.signal()
		q.mu.Unlock()
	}()
	// SetSend(nil) may race the observation. The captured function remains owned
	// through this send; takeover waits for its return.
	if send != nil {
		(*send)(delivery)
	}
}

func (s *Sink) waitApplicationCallback() {
	q := &s.queue
	q.mu.Lock()
	for q.claimed {
		changed := q.changed
		q.mu.Unlock()
		<-changed
		q.mu.Lock()
	}
	q.mu.Unlock()
}
