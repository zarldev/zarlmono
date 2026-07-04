package runner

import (
	"context"
	"sync"
	"time"
)

// iterationContext returns ctx unchanged when timeout is 0 (no
// budget), or a derived ctx that auto-cancels after timeout. The
// returned cancel is always non-nil; callers should defer it.
//
// Wrapping is per-iteration so the runner can bail one iteration on
// budget without affecting the outer Run context. Caller's outer
// timeout still wins if it fires first.
func iterationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx) // no-op timeout, but real cancel for symmetry
	}
	return context.WithTimeout(ctx, timeout)
}

// watchStreamIdle observes the last-chunk timestamp and cancels
// iterCtx when the gap exceeds threshold. Returns immediately when
// iterCtx is already done. Tick interval is threshold/4 (rounded to
// a sane minimum) so the actual cancellation lands within ~25% of the
// configured budget.
func watchStreamIdle(iterCtx context.Context, cancel context.CancelFunc, last *atomicTime, threshold time.Duration) {
	tick := max(threshold/4, time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-iterCtx.Done():
			return
		case <-t.C:
			if time.Since(last.Get()) >= threshold {
				cancel()
				return
			}
		}
	}
}

// atomicTime is a minimal mutex-guarded time.Time. Used in the
// stream-idle watchdog so the consumer goroutine can update without
// racing the watcher goroutine. sync/atomic.Value would also work
// but a tiny mutex keeps the type concrete and avoids interface
// boxing on every chunk.
type atomicTime struct {
	mu sync.Mutex
	t  time.Time
}

func (a *atomicTime) Set(t time.Time) {
	a.mu.Lock()
	a.t = t
	a.mu.Unlock()
}

func (a *atomicTime) Get() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}
