package events

import (
	"context"
	"log/slog"
	"time"
)

// Handler reacts to events.
type Handler interface {
	Handle(ctx context.Context, event Event) error
}

// Bus is an in-process event dispatcher.
type Bus struct {
	handlers map[EventType][]Handler
	ch       chan Event
	stop     chan struct{}
}

// New creates a Bus with the given channel buffer size.
func New(bufSize int) *Bus {
	return &Bus{
		handlers: make(map[EventType][]Handler),
		ch:       make(chan Event, bufSize),
		stop:     make(chan struct{}),
	}
}

// Register adds a handler for the given event type.
// Handlers are called in registration order.
func (b *Bus) Register(t EventType, h Handler) {
	b.handlers[t] = append(b.handlers[t], h)
}

// Emit sends an event to the bus. Non-blocking; drops if buffer is full.
func (b *Bus) Emit(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	select {
	case b.ch <- e:
	default:
		slog.Warn("event bus full, dropping event", "type", e.Type)
	}
}

// Start begins the dispatch loop in a background goroutine.
func (b *Bus) Start(ctx context.Context) {
	go b.run(ctx)
}

// Stop signals the dispatch loop to exit.
func (b *Bus) Stop() {
	close(b.stop)
}

func (b *Bus) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stop:
			return
		case e := <-b.ch:
			for _, h := range b.handlers[e.Type] {
				if err := h.Handle(ctx, e); err != nil {
					slog.Error("event handler failed",
						"type", e.Type,
						"error", err,
					)
				}
			}
		}
	}
}
