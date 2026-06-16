package tui

import "time"

// EventRingEntry is one recorded runner event with a timestamp and a brief
// human-readable kind/detail pair.
type EventRingEntry struct {
	Kind   string
	Detail string
	At     time.Time
}

// EventRing is a fixed-capacity circular buffer of recent runner events.
// It is safe for single-goroutine use (the TUI Update loop).
type EventRing struct {
	entries []EventRingEntry
	head    int
	size    int
}

// NewEventRing returns an empty ring with the given capacity.
func NewEventRing(capacity int) *EventRing {
	return &EventRing{entries: make([]EventRingEntry, capacity)}
}

// Add pushes an entry onto the ring, evicting the oldest entry when full.
func (r *EventRing) Add(e EventRingEntry) {
	if r == nil {
		return
	}
	r.entries[r.head] = e
	r.head = (r.head + 1) % len(r.entries)
	if r.size < len(r.entries) {
		r.size++
	}
}

// Snapshot returns a copy of all entries in insertion order (oldest first).
func (r *EventRing) Snapshot() []EventRingEntry {
	if r == nil || r.size == 0 {
		return nil
	}
	out := make([]EventRingEntry, 0, r.size)
	for i := range r.size {
		idx := (r.head - r.size + i) % len(r.entries)
		if idx < 0 {
			idx += len(r.entries)
		}
		out = append(out, r.entries[idx])
	}
	return out
}
