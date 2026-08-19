package scheduler_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
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
	return append([]string(nil), e.seen...)
}

func TestScheduler_FiresOnSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enq := &stubEnqueuer{}
		sch := scheduler.New(&stubSource{triggers: []scheduler.Trigger{{
			ID:       "every-second",
			Schedule: "@every 1s",
			OnFire:   func(context.Context) (string, error) { return "instance", nil },
		}}}, enq)
		if err := sch.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(sch.Stop)

		time.Sleep(time.Second)
		synctest.Wait()
		if enq.Count() != 1 {
			t.Fatalf("enqueues = %d, want 1", enq.Count())
		}
	})
}

func TestScheduler_InvalidScheduleSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enq := &stubEnqueuer{}
		sch := scheduler.New(&stubSource{triggers: []scheduler.Trigger{
			{ID: "bad", Schedule: "not a cron expression", OnFire: func(context.Context) (string, error) { return "x", nil }},
			{ID: "good", Schedule: "@every 100ms", OnFire: func(context.Context) (string, error) { return "good-instance", nil }},
		}}, enq)
		if err := sch.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(sch.Stop)

		time.Sleep(time.Second)
		synctest.Wait()
		for _, id := range enq.Snapshot() {
			if id != "good-instance" {
				t.Errorf("enqueued unexpected ID: %q", id)
			}
		}
		if enq.Count() == 0 {
			t.Fatal("the good trigger should have fired despite the bad entry")
		}
	})
}

func TestScheduler_StopIsIdempotent(t *testing.T) {
	sch := scheduler.New(&stubSource{}, &stubEnqueuer{})
	if err := sch.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sch.Stop()
	sch.Stop()
}

func TestScheduler_OnFireErrorDoesNotEnqueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enq := &stubEnqueuer{}
		fired := make(chan struct{}, 1)
		sch := scheduler.New(&stubSource{triggers: []scheduler.Trigger{{
			ID:       "errors",
			Schedule: "@every 100ms",
			OnFire: func(context.Context) (string, error) {
				fired <- struct{}{}
				return "", errTest
			},
		}}}, enq)
		if err := sch.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(sch.Stop)

		time.Sleep(100 * time.Millisecond)
		<-fired
		if enq.Count() != 0 {
			t.Errorf("Enqueue was called %d times despite OnFire returning error", enq.Count())
		}
	})
}

var errTest = newTestErr("spawn failed")

type testError struct{ msg string }

func (e *testError) Error() string     { return e.msg }
func newTestErr(msg string) *testError { return &testError{msg: msg} }

func TestScheduler_StopUnblocksJobBlockedOnContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobStarted := make(chan struct{})
		jobReturned := make(chan struct{})
		sch := scheduler.New(&stubSource{triggers: []scheduler.Trigger{{
			ID:       "stuck",
			Schedule: "@every 1s",
			OnFire: func(ctx context.Context) (string, error) {
				jobStarted <- struct{}{}
				<-ctx.Done()
				close(jobReturned)
				return "", ctx.Err()
			},
		}}}, &stubEnqueuer{})
		if err := sch.Start(t.Context()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		time.Sleep(time.Second)
		<-jobStarted
		done := make(chan struct{})
		go func() {
			sch.Stop()
			close(done)
		}()
		<-done
		<-jobReturned
	})
}
