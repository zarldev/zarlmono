package sensor

import (
	"context"
	"log/slog"
	"time"
)

// Start begins polling each sensor on its own goroutine. Safe to call
// once. Cancel ctx or call Stop to shut down. Polling receives a
// context derived from both the caller ctx and the runner ctx, so Stop
// cancels context-aware Poll calls even if the caller ctx remains live.
// Stop waits for poll-loop goroutines up to pollShutdownCap.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.stopped {
		// Already shut down: spawning poll loops here would run on the
		// cancelled runner ctx (exiting immediately) and, worse, race a
		// pollWG.Add against the Wait in Stop — a WaitGroup-reuse panic.
		// Mirror the guard Register/RegisterReactive already enforce.
		r.mu.Unlock()
		return
	}
	sensors := append([]Sensor{}, r.sensors...)
	runnerCtx := r.ctx
	// Add to the WaitGroup while holding the lock, so a concurrent Stop
	// (which sets r.stopped under the same lock, then later Waits) can never
	// observe an Add after its Wait has begun.
	r.pollWG.Add(len(sensors))
	r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	pollCtx, cancelPoll := context.WithCancel(runnerCtx)
	go func() {
		select {
		case <-ctx.Done():
			cancelPoll()
		case <-pollCtx.Done():
		}
	}()
	for _, s := range sensors {
		go func(s Sensor) {
			defer r.pollWG.Done()
			r.loop(pollCtx, s)
		}(s)
	}
}

// Stop terminates all goroutines started by Start, signals every
// registered Reactive via its Stop method, and cancels the runner
// context so any reactive still in a ctx-aware select returns.
//
// Idempotent — a second Stop is a no-op.
func (r *Runner) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	close(r.stop)
	// Snapshot reactives + their done channels under the lock; we
	// release before calling rc.Stop / waiting on done so the
	// reactive's emit path can still acquire r.mu without
	// deadlocking against us.
	reactives := append([]Reactive(nil), r.reactives...)
	dones := make(map[string]chan struct{}, len(r.reactiveDone))
	for k, ch := range r.reactiveDone {
		dones[k] = ch
	}
	cancel := r.cancel
	r.mu.Unlock()

	// Cancel the runner-scoped context first so reactives waiting
	// on ctx.Done return. Belt-and-braces: also call rc.Stop on
	// each so subscriptions that don't observe ctx still tear down.
	if cancel != nil {
		cancel()
	}
	for _, rc := range reactives {
		rc.Stop()
	}
	// Wait for every reactive goroutine to exit, bounded by
	// reactiveShutdownCap per reactive. Without the wait, "Stop
	// returned" doesn't actually mean "no more observations will
	// fire"; without the cap, a misbehaving reactive can wedge the
	// caller's shutdown indefinitely.
	for key, done := range dones {
		select {
		case <-done:
		case <-time.After(reactiveShutdownCap):
			slog.Default().
				WarnContext(context.Background(), "sensor: reactive did not exit within shutdown cap on Stop",
					"key", key, "wait_cap", reactiveShutdownCap)
		}
	}

	// Wait for poll-loop goroutines too, bounded so a misbehaving Poll
	// that ignores ctx.Done cannot wedge shutdown forever.
	pollDone := make(chan struct{})
	go func() {
		r.pollWG.Wait()
		close(pollDone)
	}()
	select {
	case <-pollDone:
	case <-time.After(pollShutdownCap):
		slog.Default().WarnContext(context.Background(), "sensor: polling sensors did not exit within shutdown cap",
			"wait_cap", pollShutdownCap)
	}
}
