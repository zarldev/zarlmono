package tui_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartSwarmRequiresExactSession(t *testing.T) {
	f := newBeforeFixture(t)
	_ = f.ui.StartSwarm("shared goal")
	if !strings.Contains(f.ui.ToastText(), "exact-replay") {
		t.Fatalf("unexpected toast: %s", f.ui.ToastText())
	}
}
func TestSlashSwarmRoutesToStart(t *testing.T) {
	f := newBeforeFixture(t)
	_ = f.ui.Submit("/swarm shared goal")
	if !strings.Contains(f.ui.ToastText(), "exact-replay") {
		t.Fatalf("unexpected toast: %s", f.ui.ToastText())
	}
}

func TestStartSwarmSettlesRecordedActivationRejection(t *testing.T) {
	f := newBeforeFixture(t)
	var calls atomic.Int32
	f.provider.check = func(context.Context) { calls.Add(1) }
	settleRewindTurn(t, f, "seed exact session")

	_ = f.ui.StartSwarm("shared research goal")
	if !strings.Contains(f.ui.ToastText(), "swarm started") {
		t.Fatalf("swarm did not start: %s", f.ui.ToastText())
	}

	// The non-observation fixture provider is rejected before dispatch; drive the
	// event loop until the recorded activation's failure settles its receipt.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(f.ui.ToastText(), "swarm activation failed") {
		if time.Now().After(deadline) {
			t.Fatalf("activation did not settle: calls=%d toast=%s", calls.Load(), f.ui.ToastText())
		}
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		if len(events) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		for i, event := range events {
			_, cmd := f.ui.Update(event)
			if i == len(events)-1 {
				driveBeforeCommand(f, cmd)
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("rejected activation dispatched a provider call: %d", calls.Load())
	}

	// Close the goal so its owned goroutine drains before the fixture tears down.
	_ = f.ui.StopSwarm()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		if len(events) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		for _, event := range events {
			f.ui.Update(event)
		}
	}
}
