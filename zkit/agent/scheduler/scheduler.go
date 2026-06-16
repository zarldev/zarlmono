// Package scheduler runs cron-driven task triggers — recurring agent
// tasks that the runner enqueues on schedule.
//
// The scheduler is generic over the task store: consumers pass a
// TriggerSource that produces Triggers (cron expression + spawn
// callback) and an Enqueuer for queuing the spawned instances. The
// underlying cron engine is github.com/robfig/cron/v3.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// shutdownWaitCap bounds how long Stop / Reload will block waiting
// for in-flight cron jobs to finish after the derived context has
// been cancelled. Past this cap we log and proceed — the alternative
// (block forever) is worse than the alternative (a wedged job is
// left running until its own kill chain trips). Sized for our
// agent-spawn jobs which finish in seconds; bump if a consumer
// schedules genuinely long-running cron jobs.
const shutdownWaitCap = 30 * time.Second

// Trigger is a single scheduled-task entry. Spawn is invoked when the
// cron expression fires; it returns the ID of the newly-created task
// instance for the Enqueuer to pick up.
type Trigger struct {
	// ID is the trigger's stable identifier — used for log lines so
	// failures can be traced back to a row in the source store.
	ID string

	// Schedule is the cron expression. The cron library's standard
	// 5-field format is supported (minute, hour, day, month, weekday).
	Schedule string

	// OnFire is invoked when the cron fires. It typically creates a
	// new task instance in the database and returns the new instance's
	// ID. Errors are logged; the trigger continues firing on schedule.
	OnFire func(ctx context.Context) (instanceID string, err error)
}

// TriggerSource produces the triggers the scheduler should run. Called
// at Start time and on Reload; each call should return the current set
// of scheduled triggers (typically a database read).
type TriggerSource interface {
	List(ctx context.Context) ([]Trigger, error)
}

// Enqueuer is whatever puts instance IDs onto the runner's work queue.
// The scheduler doesn't care what — it just calls Enqueue with the
// instance ID returned by OnFire.
type Enqueuer interface {
	Enqueue(instanceID string)
}

// Scheduler loads triggers from a source and registers cron entries.
// Start it once; call Reload after the source's data changes (e.g.
// admin added a new scheduled task). Lifecycle methods are safe for
// concurrent calls: mu serialises Start/Reload/Stop transitions so
// admin paths firing concurrent reloads don't double-start cron or
// race against an in-flight Stop.
type Scheduler struct {
	source   TriggerSource
	enqueuer Enqueuer

	mu     sync.Mutex
	cron   *cron.Cron
	cancel context.CancelFunc // nil unless Start has been called
}

// New creates a Scheduler.
func New(source TriggerSource, enqueuer Enqueuer) *Scheduler {
	return &Scheduler{
		source:   source,
		enqueuer: enqueuer,
	}
}

// Start loads triggers from the source and registers a cron entry for
// each. When an entry fires, OnFire is invoked and the returned
// instance ID is passed to the Enqueuer.
//
// The supplied context is wrapped with a derived cancel; call Stop
// (or cancel the parent context) to halt the scheduler and cancel
// all in-flight OnFire goroutines. Returns an error if the scheduler
// is already running — call Stop first or use Reload.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		return errors.New("scheduler: already started — call Stop or Reload")
	}
	return s.startLocked(ctx)
}

// startLocked is the body of Start without locking. Callers must
// hold s.mu and have verified s.cron == nil.
func (s *Scheduler) startLocked(ctx context.Context) error {
	s.cron = cron.New()
	derivedCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	triggers, err := s.source.List(derivedCtx)
	if err != nil {
		// Roll back partial state so a failed Start leaves the
		// scheduler in the "not started" position (cron == nil)
		// rather than a half-built one.
		cancel()
		s.cron = nil
		s.cancel = nil
		return err
	}

	for _, t := range triggers {
		// capture for closure
		_, err := s.cron.AddFunc(t.Schedule, func() {
			id, err := t.OnFire(derivedCtx)
			if err != nil {
				slog.ErrorContext(derivedCtx, "scheduler: trigger fire failed",
					"trigger_id", t.ID, "err", err)
				return
			}
			slog.InfoContext(derivedCtx, "scheduler: trigger fired",
				"trigger_id", t.ID, "instance_id", id)
			s.enqueuer.Enqueue(id)
		})
		if err != nil {
			slog.ErrorContext(derivedCtx, "scheduler: register entry",
				"id", t.ID, "schedule", t.Schedule, "err", err)
			continue
		}
		slog.InfoContext(derivedCtx, "scheduler: registered entry",
			"id", t.ID, "schedule", t.Schedule)
	}

	s.cron.Start()
	return nil
}

// Reload stops the cron engine, waits for in-flight jobs to finish,
// and restarts with fresh triggers from the source. Holding the
// mutex across the whole transition prevents two reloads from
// racing — earlier the cron.Stop / Start sequence wasn't
// serialised so a concurrent reload could leave duplicate cron
// entries running.
func (s *Scheduler) Reload(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.shutdownLocked(ctx)
	}
	if err := s.startLocked(ctx); err != nil {
		slog.ErrorContext(ctx, "scheduler: reload", "err", err)
	}
}

// Stop halts the scheduler. Cancels all in-flight OnFire goroutines
// and waits for the cron engine to finish running jobs. Idempotent —
// safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron == nil {
		return
	}
	s.shutdownLocked(context.Background())
}

// shutdownLocked is the shared body of Stop / Reload's stop half.
// Caller holds s.mu and has verified s.cron != nil. The ctx is used
// only for the bounded-wait warning emission (slog correlation IDs,
// trace spans) — the wait itself uses an independent timer because
// Stop's caller-less path has no useful ctx to honour for the
// shutdown deadline.
//
// Order matters: cancel FIRST so any job blocked on the derived
// context wakes up immediately, THEN wait for cron's in-flight jobs
// to drain. Earlier shape waited on cron.Stop()'s context before
// cancelling — if a job was parked on ctx.Done() the wait would
// never finish because cron's "done" signal depends on the job
// returning, and the job was depending on the context to cancel.
// Classic deadlock cycle.
//
// The post-cancel wait is bounded by shutdownWaitCap so a job that
// ignores context cancellation can't wedge shutdown indefinitely.
// Hitting the cap is logged at Warn so it's visible without crashing
// the host process.
func (s *Scheduler) shutdownLocked(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
	}
	stopCtx := s.cron.Stop()
	timer := time.NewTimer(shutdownWaitCap)
	defer timer.Stop()
	select {
	case <-stopCtx.Done():
	case <-timer.C:
		slog.WarnContext(ctx, "scheduler: in-flight jobs did not finish within shutdown cap",
			"wait_cap", shutdownWaitCap)
	}
	s.cron = nil
	s.cancel = nil
}
