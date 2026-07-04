package sensor

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func (r *Runner) loopReactive(ctx context.Context, rc Reactive) {
	emit := func(obs Observation) {
		r.mu.Lock()
		handlers := append([]Handler{}, r.handlers...)
		r.mu.Unlock()
		for _, h := range handlers {
			h(ctx, rc.Key(), obs)
		}
	}
	if err := rc.Start(ctx, emit); err != nil {
		slog.Default().WarnContext(ctx, "reactive sensor exited", "key", rc.Key(), "err", err)
	}
}

func (r *Runner) loop(ctx context.Context, s Sensor) {
	interval := max(s.Interval(), 100*time.Millisecond)
	// Slight jitter on the first tick so a fleet of sensors doesn't
	// all fire the instant Start returns. Cap the jitter at the
	// interval itself so sub-second test sensors aren't starved.
	initialDelay := 200 * time.Millisecond
	if initialDelay > interval {
		initialDelay = interval / 2
	}
	initial := time.NewTimer(initialDelay)
	select {
	case <-ctx.Done():
		initial.Stop()
		return
	case <-r.stop:
		initial.Stop()
		return
	case <-initial.C:
	}
	r.tick(ctx, s)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-t.C:
			r.tick(ctx, s)
		}
	}
}

func (r *Runner) tick(ctx context.Context, s Sensor) {
	obs, err := s.Poll(ctx)
	if ctx.Err() != nil {
		return
	}
	if errors.Is(err, ErrNoChange) {
		return
	}
	if err != nil {
		slog.Default().WarnContext(ctx, "sensor poll failed", "key", s.Key(), "err", err)
		return
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	handlers := append([]Handler{}, r.handlers...)
	r.mu.Unlock()
	for _, h := range handlers {
		h(ctx, s.Key(), obs)
	}
}
