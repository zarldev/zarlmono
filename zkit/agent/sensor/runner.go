package sensor

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Runner owns the goroutines that poll each registered sensor on its
// own schedule and fan changed observations out to handlers.
type Runner struct {
	mu        sync.Mutex
	sensors   []Sensor
	reactives []Reactive
	running   map[string]bool
	handlers  []Handler
	stop      chan struct{}
	stopped   bool
	// reactiveDone signals when each reactive goroutine has exited.
	// Keyed by the reactive's Key().
	reactiveDone map[string]chan struct{}
	// pollWG tracks the poll-loop goroutines started by Start. Stop
	// waits on it so a slow Poll() can't fire a handler after Stop
	// returns. Earlier shape relied on the stop channel to halt the
	// loop, but a goroutine mid-Poll wouldn't observe the close
	// until the Poll returned.
	pollWG sync.WaitGroup
	// ctx is the runner-scoped cancellable context every reactive
	// inherits. Stop calls cancel so reactive subscriptions tear
	// down cleanly — earlier RegisterReactive passed
	// context.Background() which left reactives running past
	// Runner.Stop, leaking goroutines + bus subscriptions.
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a Runner. The internal context lives for the runner's
// lifetime; Stop cancels it so reactives observe ctx.Done().
func New() *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		stop:         make(chan struct{}),
		running:      map[string]bool{},
		reactiveDone: map[string]chan struct{}{},
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Register adds a poll sensor. Returns [ErrRunnerStopped] when the
// runner has already been stopped. Must otherwise be called before
// Start (or alongside RegisterReactive's hot-activation path).
func (r *Runner) Register(s Sensor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrRunnerStopped
	}
	r.sensors = append(r.sensors, s)
	r.running[s.Key()] = true
	return nil
}

// RegisterReactive adds an event-driven sensor and kicks off its Start
// goroutine immediately. Unlike poll sensors, reactives hold long-lived
// subscriptions so they begin running the moment they're registered.
//
// Returns [ErrRunnerStopped] if the runner has been stopped — earlier
// shape spawned the goroutine anyway, which leaked past Runner.Stop.
func (r *Runner) RegisterReactive(rc Reactive) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return ErrRunnerStopped
	}
	r.reactives = append(r.reactives, rc)
	r.running[rc.Key()] = true
	done := make(chan struct{})
	r.reactiveDone[rc.Key()] = done
	r.mu.Unlock()
	go func() {
		defer close(done)
		// Use the runner-scoped context so Runner.Stop tears the
		// reactive down. The reactive's Start should select on
		// ctx.Done to honour cancellation.
		r.loopReactive(r.ctx, rc)
	}()
	return nil
}

// IsRunning reports whether a sensor with the given key is currently
// registered (poll or reactive). Used by controllers to short-circuit
// redundant activations.
func (r *Runner) IsRunning(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[key]
}

// Remove unregisters a sensor by key. For reactive sensors, Stop is
// invoked so the subscription tears down. Returns true when something
// was actually removed.
func (r *Runner) Remove(key string) bool {
	r.mu.Lock()
	if !r.running[key] {
		r.mu.Unlock()
		return false
	}
	delete(r.running, key)
	// Drop from poll list.
	filtered := r.sensors[:0]
	for _, s := range r.sensors {
		if s.Key() != key {
			filtered = append(filtered, s)
		}
	}
	r.sensors = filtered
	// Drop from reactive list + stop the goroutine. Wait for it to exit.
	remaining := r.reactives[:0]
	var done <-chan struct{}
	for _, rc := range r.reactives {
		if rc.Key() != key {
			remaining = append(remaining, rc)
			continue
		}
		rc.Stop()
		done = r.reactiveDone[rc.Key()]
	}
	r.reactives = remaining
	r.mu.Unlock()
	// Wait for the reactive goroutine to exit before returning.
	// This prevents races if the caller then modifies r.handlers.
	// Bounded by reactiveShutdownCap so a misbehaving reactive that
	// ignores ctx.Done can't wedge Remove indefinitely.
	if done != nil {
		select {
		case <-done:
		case <-time.After(reactiveShutdownCap):
			slog.Default().
				WarnContext(context.Background(), "sensor: reactive did not exit within shutdown cap on Remove",
					"key", key, "wait_cap", reactiveShutdownCap)
		}
	}
	return true
}

// OnChange installs a handler invoked when any sensor reports a change.
// Multiple handlers are supported (each sees every change in the order
// they were added).
func (r *Runner) OnChange(h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
}
