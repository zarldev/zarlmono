package pursue_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
)

// closedWithin reports whether ch closes within d.
func closedWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestPollWatcher_FiresWhenProbeTrueImmediately(t *testing.T) {
	t.Parallel()
	w := pursue.PollWatcher(func(context.Context) bool { return true }, time.Millisecond)
	ch := w(t.Context())
	if !closedWithin(ch, time.Second) {
		t.Fatal("watcher should fire immediately when probe is already true")
	}
}

func TestPollWatcher_FiresAfterProbeFlips(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	// Flip to true on the 3rd call so the ticker path (not just the initial
	// check) is exercised.
	probe := func(context.Context) bool { return calls.Add(1) >= 3 }
	w := pursue.PollWatcher(probe, time.Millisecond)
	ch := w(t.Context())
	if !closedWithin(ch, time.Second) {
		t.Fatal("watcher should fire once the probe flips true")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("probe called %d times, want >= 3", got)
	}
}

func TestPollWatcher_NeverFiresWhenProbeFalse_ExitsOnCancel(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	probe := func(context.Context) bool { calls.Add(1); return false }
	ctx, cancel := context.WithCancel(t.Context())
	w := pursue.PollWatcher(probe, time.Millisecond)
	ch := w(ctx)
	// Stays open while the probe keeps saying false.
	if closedWithin(ch, 50*time.Millisecond) {
		t.Fatal("watcher must not fire while probe returns false")
	}
	cancel()
	// After cancel the goroutine exits without closing the channel
	// (cancellation is not goal-met).
	if closedWithin(ch, 100*time.Millisecond) {
		t.Fatal("watcher must not close its channel on ctx cancellation")
	}
	if calls.Load() == 0 {
		t.Error("probe should have been polled at least once")
	}
}

func TestPollWatcher_NilProbeNeverFires(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	ch := pursue.PollWatcher(nil, time.Millisecond)(ctx)
	if closedWithin(ch, 50*time.Millisecond) {
		t.Fatal("nil-probe watcher must never fire")
	}
	cancel() // goroutine exits on ctx.Done; channel stays open
	if closedWithin(ch, 100*time.Millisecond) {
		t.Fatal("nil-probe watcher must not close on cancel")
	}
}

func TestPollWatcher_ZeroIntervalUsesDefault(t *testing.T) {
	t.Parallel()
	// interval <= 0 must not panic (NewTicker(0) panics) — it falls back to
	// DefaultPollInterval. A probe that's true immediately still fires.
	ch := pursue.PollWatcher(func(context.Context) bool { return true }, 0)(t.Context())
	if !closedWithin(ch, time.Second) {
		t.Fatal("zero interval should use the default, not panic")
	}
}
