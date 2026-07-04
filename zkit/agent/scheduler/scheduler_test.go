package scheduler_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/scheduler"
)

// stubSource returns a fixed set of triggers.
type stubSource struct {
	triggers []scheduler.Trigger
}

func (s *stubSource) List(_ context.Context) ([]scheduler.Trigger, error) {
	return s.triggers, nil
}

// stubEnqueuer captures Enqueue calls.
type stubEnqueuer struct {
	mu   sync.Mutex
	seen []string
}

func (e *stubEnqueuer) Enqueue(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, id)
}

func (e *stubEnqueuer) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.seen)
}

func (e *stubEnqueuer) Snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.seen))
	copy(out, e.seen)
	return out
}

func TestScheduler_FiresOnSchedule(t *testing.T) {
	t.Parallel()

	enq := &stubEnqueuer{}
	var fired sync.Map
	src := &stubSource{
		triggers: []scheduler.Trigger{{
			ID:       "every-second",
			Schedule: "@every 1s",
			OnFire: func(ctx context.Context) (string, error) {
				id := "instance-" + time.Now().Format("150405.000")
				fired.Store(id, true)
				return id, nil
			},
		}},
	}

	sch := scheduler.New(src, enq)
	if err := sch.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sch.Stop)

	// Wait for the trigger to fire at least once. @every 1s fires
	// approximately one second after registration.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if enq.Count() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if enq.Count() < 1 {
		t.Fatalf("expected at least 1 enqueue within 3s, got %d", enq.Count())
	}
}

func TestScheduler_InvalidScheduleSkipped(t *testing.T) {
	t.Parallel()

	enq := &stubEnqueuer{}
	src := &stubSource{
		triggers: []scheduler.Trigger{
			{ID: "bad", Schedule: "not a cron expression", OnFire: func(context.Context) (string, error) {
				return "x", nil
			}},
			{ID: "good", Schedule: "@every 100ms", OnFire: func(context.Context) (string, error) {
				return "good-instance", nil
			}},
		},
	}

	sch := scheduler.New(src, enq)
	// Start succeeds even though one entry is malformed — the bad one is
	// skipped with an error log.
	if err := sch.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sch.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if enq.Count() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	for _, id := range enq.Snapshot() {
		if id != "good-instance" {
			t.Errorf("enqueued unexpected ID: %q", id)
		}
	}
	if enq.Count() < 1 {
		t.Fatal("the good trigger should have fired despite the bad entry")
	}
}

func TestScheduler_StopIsIdempotent(t *testing.T) {
	t.Parallel()

	src := &stubSource{}
	sch := scheduler.New(src, &stubEnqueuer{})
	if err := sch.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sch.Stop()
	sch.Stop() // must not panic
}

func TestScheduler_OnFireErrorDoesNotEnqueue(t *testing.T) {
	t.Parallel()

	enq := &stubEnqueuer{}
	src := &stubSource{
		triggers: []scheduler.Trigger{{
			ID:       "errors",
			Schedule: "@every 100ms",
			OnFire: func(context.Context) (string, error) {
				return "", errTest
			},
		}},
	}

	sch := scheduler.New(src, enq)
	if err := sch.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sch.Stop)

	time.Sleep(500 * time.Millisecond)

	if enq.Count() > 0 {
		t.Errorf("Enqueue was called %d times despite OnFire returning error", enq.Count())
	}
}

var errTest = newTestErr("spawn failed")

type testError struct{ msg string }

func (e *testError) Error() string     { return e.msg }
func newTestErr(msg string) *testError { return &testError{msg: msg} }

// TestScheduler_StopUnblocksJobBlockedOnContext is the regression
// test for the cancel-before-wait deadlock. A trigger that parks on
// ctx.Done would previously hang Stop forever: Stop waited on
// cron.Stop()'s context (which doesn't return until the job
// returns), and the job was waiting on the derived ctx (which
// wasn't cancelled until AFTER cron.Stop completed). Classic AB/BA
// — minus the second lock — that we now break by cancelling first
// and only then waiting on cron.
func TestScheduler_StopUnblocksJobBlockedOnContext(t *testing.T) {
	t.Parallel()

	jobStarted := make(chan struct{})
	jobReturned := make(chan struct{})

	enq := &stubEnqueuer{}
	src := &stubSource{triggers: []scheduler.Trigger{{
		ID:       "stuck",
		Schedule: "@every 1s", // fires within the test window
		OnFire: func(ctx context.Context) (string, error) {
			select {
			case jobStarted <- struct{}{}:
			default:
			}
			// Block until ctx fires. Without the cancel-first fix,
			// this never returns because Stop won't cancel ctx until
			// cron.Stop returns, which won't return until OnFire
			// returns.
			<-ctx.Done()
			close(jobReturned)
			return "", ctx.Err()
		},
	}}}

	s := scheduler.New(src, enq)
	if err := s.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the job to actually start so we know the stuck
	// goroutine exists when Stop is called.
	select {
	case <-jobStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("trigger never fired")
	}

	// Stop must return within the shutdown cap even with the job
	// parked on ctx.Done. Give it a generous wall budget so a slow
	// CI doesn't false-positive — but the real bug would have hung
	// forever, so any finite bound catches the regression.
	stopReturned := make(chan struct{})
	go func() {
		s.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
	case <-time.After(40 * time.Second):
		t.Fatal("Stop deadlocked — cancel-before-wait regression")
	}

	// Job's ctx.Done branch should have fired.
	select {
	case <-jobReturned:
	case <-time.After(1 * time.Second):
		t.Error("OnFire never observed ctx.Done after Stop returned")
	}
}
