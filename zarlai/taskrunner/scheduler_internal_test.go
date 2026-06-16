package taskrunner

import (
	"context"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
)

type fakeSchedTasks struct {
	scheduled []repository.Task
}

func (f *fakeSchedTasks) ListScheduled(context.Context) ([]repository.Task, error) {
	return f.scheduled, nil
}

func (f *fakeSchedTasks) Create(context.Context, string, string, string, string, string, string, int) (repository.Task, error) {
	return repository.Task{ID: "instance"}, nil
}

// TestScheduler_ReloadKeepsLiveBaseCtx locks the dead-context fix: the first
// Start wins the long-lived job context, and a later Reload carrying an
// already-cancelled (tool-call) ctx must NOT replace it — otherwise every
// scheduled task would fire against a cancelled context and silently never run.
func TestScheduler_ReloadKeepsLiveBaseCtx(t *testing.T) {
	t.Parallel()
	s := NewScheduler(&fakeSchedTasks{scheduled: []repository.Task{{ID: "a", Schedule: "@every 1h"}}}, nil)

	appCtx := context.Background() // long-lived, like main's
	if err := s.Start(appCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadCtx, cancel := context.WithCancel(context.Background())
	cancel() // the schedule_task tool's ctx is already cancelled by fire time
	s.Reload(deadCtx)

	if s.baseCtx != appCtx {
		t.Fatal("Reload clobbered baseCtx with the caller's ctx — scheduled tasks would never fire")
	}
	if s.baseCtx.Err() != nil {
		t.Fatalf("job context is cancelled (%v) — Create would return immediately", s.baseCtx.Err())
	}
}

// TestScheduler_ConcurrentReloadNoRace exercises the mutex: concurrent Reloads
// must not race the s.cron field (run with -race). The instant fake makes the
// window tight.
func TestScheduler_ConcurrentReloadNoRace(t *testing.T) {
	t.Parallel()
	s := NewScheduler(&fakeSchedTasks{}, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Reload(context.Background())
		}()
	}
	wg.Wait()
	s.Stop()
}
