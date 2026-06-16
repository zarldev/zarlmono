package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlai/events"
)

type spyHandler struct {
	mu       sync.Mutex
	received []events.Event
}

func (s *spyHandler) Handle(_ context.Context, e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, e)
	return nil
}

func (s *spyHandler) events() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]events.Event{}, s.received...)
}

func TestBus_Emit_dispatches_to_registered_handlers(t *testing.T) {
	ctx := t.Context()
	bus := events.New(64)
	spy := &spyHandler{}

	bus.Register(events.SessionEnded, spy)
	bus.Start(ctx)
	defer bus.Stop()

	bus.Emit(events.Event{
		Type:    events.SessionEnded,
		Payload: "test-payload",
	})

	time.Sleep(50 * time.Millisecond)
	got := spy.events()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != events.SessionEnded {
		t.Errorf("expected SessionEnded, got %s", got[0].Type)
	}
}

func TestBus_Emit_only_dispatches_to_matching_handlers(t *testing.T) {
	ctx := t.Context()
	bus := events.New(64)
	spy := &spyHandler{}

	bus.Register(events.TaskFinding, spy)
	bus.Start(ctx)
	defer bus.Stop()

	bus.Emit(events.Event{Type: events.SessionEnded, Payload: "ignored"})

	time.Sleep(50 * time.Millisecond)
	got := spy.events()
	if len(got) != 0 {
		t.Fatalf("expected 0 events, got %d", len(got))
	}
}

func TestBus_multiple_handlers_same_event(t *testing.T) {
	ctx := t.Context()
	bus := events.New(64)
	spy1 := &spyHandler{}
	spy2 := &spyHandler{}

	bus.Register(events.SessionEnded, spy1)
	bus.Register(events.SessionEnded, spy2)
	bus.Start(ctx)
	defer bus.Stop()

	bus.Emit(events.Event{Type: events.SessionEnded, Payload: "both"})

	time.Sleep(50 * time.Millisecond)
	if len(spy1.events()) != 1 {
		t.Errorf("spy1: expected 1 event, got %d", len(spy1.events()))
	}
	if len(spy2.events()) != 1 {
		t.Errorf("spy2: expected 1 event, got %d", len(spy2.events()))
	}
}
