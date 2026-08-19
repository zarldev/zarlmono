package sensor

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Start after Stop must be a no-op: it must not spawn a poll loop (which would
// run on the cancelled runner ctx) and must not Add to the WaitGroup after
// Stop's Wait (a reuse panic).
func TestRunnerStartAfterStopIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{}, 1)
		s := NewFunc("after-stop", time.Hour, func(context.Context) (Observation, error) {
			pollStarted <- struct{}{}
			return Observation{}, nil
		})
		r := New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.Stop()
		r.Start(t.Context())
		synctest.Wait()
		select {
		case <-pollStarted:
			t.Fatal("poll ran after Stop")
		default:
		}
	})
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
	go func() { defer wg.Done(); r.Start(t.Context()) }()
	go func() { defer wg.Done(); r.Stop() }()
	wg.Wait()
}

func TestRunnerStopCancelsContextAwarePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{})
		pollCanceled := make(chan struct{})
		s := NewFunc("blocking", 100*time.Millisecond, func(ctx context.Context) (Observation, error) {
			pollStarted <- struct{}{}
			<-ctx.Done()
			close(pollCanceled)
			return Observation{}, ctx.Err()
		})

		r := New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.Start(t.Context())
		time.Sleep(100 * time.Millisecond)
		<-pollStarted

		done := make(chan struct{})
		go func() {
			r.Stop()
			close(done)
		}()
		<-pollCanceled
		<-done
	})
}

func TestRunnerStopBoundedForContextIgnoringPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oldCap := pollShutdownCap
		pollShutdownCap = 20 * time.Millisecond
		t.Cleanup(func() { pollShutdownCap = oldCap })

		pollStarted := make(chan struct{})
		releasePoll := make(chan struct{})
		handlerCalled := make(chan struct{}, 1)
		s := NewFunc("stubborn", 100*time.Millisecond, func(context.Context) (Observation, error) {
			pollStarted <- struct{}{}
			<-releasePoll
			return Observation{Value: "late"}, nil
		})

		r := New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.OnChange(func(context.Context, string, Observation) { handlerCalled <- struct{}{} })
		r.Start(t.Context())
		time.Sleep(100 * time.Millisecond)
		<-pollStarted

		r.Stop()
		close(releasePoll)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		select {
		case <-handlerCalled:
			t.Fatal("handler fired after Stop returned")
		default:
		}
	})
}
