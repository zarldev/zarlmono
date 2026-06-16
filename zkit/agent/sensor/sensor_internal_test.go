package sensor

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Start after Stop must be a no-op: it must not spawn a poll loop (which would
// run on the cancelled runner ctx) and must not Add to the WaitGroup after
// Stop's Wait (a reuse panic).
func TestRunnerStartAfterStopIsNoop(t *testing.T) {
	s := NewFunc("after-stop", time.Hour, func(context.Context) (Observation, error) {
		t.Error("poll must not run after Stop")
		return Observation{}, nil
	})
	r := New()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Stop()
	r.Start(context.Background()) // must not panic, must not spawn
	time.Sleep(50 * time.Millisecond)
}

// Concurrent Start/Stop must be race-free (run under -race). The Add-under-lock
// + stopped guard is what makes this safe.
func TestRunnerConcurrentStartStop(t *testing.T) {
	s := NewFunc("race", time.Hour, func(context.Context) (Observation, error) {
		return Observation{}, nil
	})
	r := New()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.Start(context.Background()) }()
	go func() { defer wg.Done(); r.Stop() }()
	wg.Wait()
}

func TestRunnerStopCancelsContextAwarePoll(t *testing.T) {
	oldCap := pollShutdownCap
	pollShutdownCap = time.Second
	t.Cleanup(func() { pollShutdownCap = oldCap })

	pollStarted := make(chan struct{})
	pollCanceled := make(chan struct{})
	s := NewFunc("blocking", 100*time.Millisecond, func(ctx context.Context) (Observation, error) {
		close(pollStarted)
		<-ctx.Done()
		close(pollCanceled)
		return Observation{}, ctx.Err()
	})

	r := New()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Start(context.Background())

	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()

	select {
	case <-pollCanceled:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel poll context")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after poll observed cancellation")
	}
}

func TestRunnerStopBoundedForContextIgnoringPoll(t *testing.T) {
	oldCap := pollShutdownCap
	pollShutdownCap = 20 * time.Millisecond
	t.Cleanup(func() { pollShutdownCap = oldCap })

	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	handlerCalled := make(chan struct{}, 1)
	s := NewFunc("stubborn", 100*time.Millisecond, func(context.Context) (Observation, error) {
		close(pollStarted)
		<-releasePoll
		return Observation{Value: "late"}, nil
	})

	r := New()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.OnChange(func(context.Context, string, Observation) {
		select {
		case handlerCalled <- struct{}{}:
		default:
		}
	})
	r.Start(context.Background())

	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}

	start := time.Now()
	r.Stop()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop took %s, want bounded return", elapsed)
	}

	close(releasePoll)
	select {
	case <-handlerCalled:
		t.Fatal("handler fired after Stop returned")
	case <-time.After(50 * time.Millisecond):
	}
}
