package pursue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrSupervisorClosed identifies reuse or launch after a supervisor is sealed.
var ErrSupervisorClosed = errors.New("pursuit supervisor closed")

// Supervisor owns one pursuit's attempt and owned-watcher goroutines. Drive may
// return after its bounded grace; Stop and Join retain ownership of late work.
// Supervisor owns the private lifetime context used to cancel owned probes.
// Callers retain ownership of contexts passed to Drive, Join, and watcher
// invocations; each caller context bounds only its corresponding operation.
// Arbitrary Request.Watcher implementations remain externally owned.
type Supervisor struct {
	mu              sync.Mutex
	started, sealed bool
	cancel          context.CancelFunc
	lifetime        context.Context
	cancelLifetime  context.CancelFunc
	stopped         chan struct{}
	wg              sync.WaitGroup
	joined          chan struct{}
	joinOnce        sync.Once
	last            runner.TaskResult
	settled         bool
}

// NewSupervisor constructs a one-use owner without launching work.
func NewSupervisor() *Supervisor {
	lifetime, cancelLifetime := context.WithCancel(context.Background())
	return &Supervisor{
		lifetime:       lifetime,
		cancelLifetime: cancelLifetime,
		stopped:        make(chan struct{}),
		joined:         make(chan struct{}),
	}
}

// Drive runs one decision loop and seals/cancels it on return. Call Join before
// closing any dependencies borrowed by attempts or owned polling probes. Attempt
// callbacks must not panic; callers must translate panics at their boundary.
func (s *Supervisor) Drive(ctx context.Context, req Request, opts ...options.Option[Config]) Outcome {
	s.mu.Lock()
	if s.started || s.sealed {
		s.mu.Unlock()
		return Outcome{
			Result: runner.TaskResult{Reason: runner.TerminalError, Err: ErrSupervisorClosed},
			err:    ErrSupervisorClosed,
		}
	}
	s.started = true
	owned, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer s.Stop()
	opts = append(opts, func(c *Config) { c.supervisor = s })
	return Drive(owned, req, opts...)
}

// Stop atomically seals launch admission and cancels owned execution. It does not
// wait; a timed-out Join never transfers dependency ownership to another caller.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.sealed {
		s.sealed = true
		close(s.stopped)
	}
	cancel := s.cancel
	s.mu.Unlock()

	s.cancelLifetime()
	if cancel != nil {
		cancel()
	}
	s.joinOnce.Do(func() { go func() { s.wg.Wait(); close(s.joined) }() })
}

// Join stops and waits for all registered attempts and owned probes. A deadline
// bounds this caller's wait only; later Join calls can finish the same drain.
func (s *Supervisor) Join(ctx context.Context) error {
	s.Stop()
	select {
	case <-s.joined:
		return nil
	default:
	}
	select {
	case <-s.joined:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LastResult returns a detached copy of the latest settled attempt, including
// work that settled after Drive returned. It never manufactures missing usage.
func (s *Supervisor) LastResult() (runner.TaskResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAttemptResult(s.last), s.settled
}

func cloneAttemptResult(r runner.TaskResult) runner.TaskResult {
	r.Messages = llm.CloneMessages(r.Messages)
	if r.Timing.TimeToFirstOutput != nil {
		firstOutput := *r.Timing.TimeToFirstOutput
		r.Timing.TimeToFirstOutput = &firstOutput
	}
	if r.LastUsage != nil {
		u := *r.LastUsage
		r.LastUsage = &u
	}
	if r.TotalUsage != nil {
		u := *r.TotalUsage
		r.TotalUsage = &u
	}
	return r
}

func (s *Supervisor) launch(ctx context.Context, fn func()) bool {
	s.mu.Lock()
	if s.sealed || ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); fn() }()
	return true
}

func (s *Supervisor) attempt(ctx context.Context, req Request, spec runner.TaskSpec, done chan<- runner.TaskResult) bool {
	return s.launch(ctx, func() {
		result := req.Attempt(ctx, spec)
		s.mu.Lock()
		s.last = cloneAttemptResult(result)
		s.settled = true
		s.mu.Unlock()
		done <- result
	})
}

// PollWatcher creates a watcher whose probe lifetime is joined by this owner.
// A probe must honor cancellation and must not panic; Join retains ownership if
// it does not return. Invocations of the returned watcher serialize the probe.
func (s *Supervisor) PollWatcher(probe func(context.Context) bool, interval time.Duration) Watcher {
	token := make(chan struct{}, 1)
	token <- struct{}{}

	return func(ctx context.Context) <-chan struct{} {
		fired := make(chan struct{})
		operation, cancel := context.WithCancel(ctx)
		if !s.launch(operation, func() {
			ownerCancelDone := make(chan struct{})
			stopOwnerCancel := context.AfterFunc(s.lifetime, func() {
				cancel()
				close(ownerCancelDone)
			})
			defer func() {
				if !stopOwnerCancel() {
					<-ownerCancelDone
				}
				cancel()
			}()

			select {
			case <-s.stopped:
				return
			case <-operation.Done():
				return
			case <-token:
			}
			defer func() { token <- struct{}{} }()

			select {
			case <-s.stopped:
				return
			case <-operation.Done():
				return
			default:
			}
			pollProbe(operation, probe, interval, fired)
		}) {
			cancel()
		}
		return fired
	}
}

func pollProbe(ctx context.Context, probe func(context.Context) bool, interval time.Duration, fired chan<- struct{}) {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	if probe == nil {
		<-ctx.Done()
		return
	}
	if ctx.Err() != nil {
		return
	}
	if probe(ctx) {
		close(fired)
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if probe(ctx) {
				close(fired)
				return
			}
		}
	}
}
