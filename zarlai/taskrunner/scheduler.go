package taskrunner

import (
	"context"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
	"github.com/zarldev/zarlmono/zarlai/repository"
)

// schedulerTasks is the consumer-side view of *repository.TaskRepo the
// Scheduler needs — narrow so the cron/context wiring is testable without a
// live DB. *repository.TaskRepo satisfies it.
type schedulerTasks interface {
	ListScheduled(ctx context.Context) ([]repository.Task, error)
	Create(ctx context.Context, prompt, personName, sessionID, schedule, profileName, workspaceName string, maxIterations int) (repository.Task, error)
}

// Scheduler loads recurring tasks from the database and enqueues new task
// instances when their cron expressions fire.
type Scheduler struct {
	tasks  schedulerTasks
	runner *Runner

	mu sync.Mutex
	// cron is rebuilt on every (Re)Start; guarded by mu so concurrent
	// Reload/Start/Stop can't race the field or orphan a *cron.Cron (and
	// leak its goroutine).
	cron *cron.Cron
	// baseCtx is the long-lived context fired jobs run under. The FIRST Start
	// wins it (main passes the app context); later Reloads — which carry a
	// short-lived tool-call ctx — must NOT clobber it, or every scheduled task
	// would fire against an already-cancelled context and silently never run.
	baseCtx context.Context
}

// NewScheduler creates a Scheduler backed by the given repo and runner.
func NewScheduler(tasks schedulerTasks, runner *Runner) *Scheduler {
	return &Scheduler{
		tasks:  tasks,
		runner: runner,
	}
}

// Start loads scheduled tasks from the database and registers a cron entry for
// each one. When an entry fires it creates a one-shot task instance and
// enqueues it for execution.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked(ctx)
}

// startLocked rebuilds the cron from the database. Caller must hold s.mu.
// loadCtx scopes the (quick) ListScheduled query; fired jobs run under the
// long-lived baseCtx, never under loadCtx.
func (s *Scheduler) startLocked(loadCtx context.Context) error {
	if s.baseCtx == nil {
		s.baseCtx = loadCtx
	}
	jobCtx := s.baseCtx

	// Stop the previous cron before replacing it so its run goroutine doesn't leak.
	if s.cron != nil {
		s.cron.Stop()
	}
	s.cron = cron.New()

	scheduled, err := s.tasks.ListScheduled(loadCtx)
	if err != nil {
		return err
	}

	for _, t := range scheduled {
		_, err := s.cron.AddFunc(t.Schedule, func() {
			// jobCtx is the long-lived app context — NOT the Start/Reload
			// caller's (possibly already-cancelled) ctx.
			instance, err := s.tasks.Create(jobCtx, t.Prompt, t.PersonName, t.SessionID, "", t.ProfileName, "", t.MaxIterations)
			if err != nil {
				slog.ErrorContext(jobCtx, "scheduler: create task instance", "schedule_id", t.ID, "err", err)
				return
			}
			slog.InfoContext(jobCtx, "scheduler: task fired", "schedule_id", t.ID, "instance_id", instance.ID)
			s.runner.Enqueue(repository.TaskID(instance.ID))
		})
		if err != nil {
			slog.ErrorContext(loadCtx, "scheduler: add cron entry", "id", t.ID, "schedule", t.Schedule, "err", err)
			continue
		}
		slog.InfoContext(loadCtx, "scheduler: registered entry", "id", t.ID, "schedule", t.Schedule)
	}

	s.cron.Start()
	return nil
}

// Reload stops the scheduler and restarts it with fresh data from the database.
// Call this after a new scheduled task is added. Fired jobs keep running under
// the original long-lived base context, not the caller's.
func (s *Scheduler) Reload(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.startLocked(ctx); err != nil {
		slog.ErrorContext(ctx, "scheduler: reload", "err", err)
	}
}

// Stop halts the scheduler, waiting for any running jobs to complete.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
	}
}
