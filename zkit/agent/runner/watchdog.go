package runner

import (
	"context"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// iterationContext creates the context that bounds one provider invocation.
// A configured deadline carries ErrIterationTimeout as its explicit cause;
// otherwise the derived cancellation is only for deterministic cleanup.
func iterationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, timeout, ErrIterationTimeout)
}

// withStreamIdle synchronously wraps stream with idle observation. Idle time is
// upstream-controlled time before the first observation and between
// observations; time in downstream yield is excluded. The invocation owns,
// stops, and joins its watchdog goroutine.
func withStreamIdle(
	cancel context.CancelCauseFunc,
	threshold time.Duration,
	stream llm.CompletionStream,
) llm.CompletionStream {
	if threshold <= 0 {
		return stream
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		watch := newIdleWatchdog(cancel, threshold)
		defer watch.stopAndWait()

		watch.arm()
		stream(func(chunk llm.CompletionChunk, err error) bool {
			watch.pause()
			accepted := yield(chunk, err)
			if !accepted || err != nil {
				return false
			}
			watch.arm()
			return true
		})
	}
}

type idleWatchdog struct {
	cancel    context.CancelCauseFunc
	threshold time.Duration

	mu       sync.Mutex
	armed    bool
	deadline time.Time
	stopped  bool
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
}

func newIdleWatchdog(cancel context.CancelCauseFunc, threshold time.Duration) *idleWatchdog {
	w := &idleWatchdog{
		cancel:    cancel,
		threshold: threshold,
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *idleWatchdog) arm() {
	w.mu.Lock()
	if !w.stopped {
		w.armed = true
		w.deadline = time.Now().Add(w.threshold)
	}
	w.mu.Unlock()
	w.signal()
}

func (w *idleWatchdog) pause() {
	w.mu.Lock()
	w.armed = false
	w.mu.Unlock()
	w.signal()
}

func (w *idleWatchdog) stopAndWait() {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.stop)
	}
	w.mu.Unlock()
	<-w.done
}

func (w *idleWatchdog) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *idleWatchdog) run() {
	defer close(w.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		w.mu.Lock()
		armed := w.armed
		deadline := w.deadline
		w.mu.Unlock()

		var timerC <-chan time.Time
		if armed {
			delay := time.Until(deadline)
			if delay < 0 {
				delay = 0
			}
			timer.Reset(delay)
			timerC = timer.C
		}

		select {
		case <-w.stop:
			return
		case <-w.wake:
			if timerC != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timerC:
			w.mu.Lock()
			expired := w.armed && !w.stopped && !time.Now().Before(w.deadline)
			if expired {
				w.armed = false
			}
			w.mu.Unlock()
			if expired {
				w.cancel(ErrStreamIdle)
				return
			}
		}
	}
}
