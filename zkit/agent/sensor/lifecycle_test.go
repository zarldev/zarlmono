package sensor_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/sensor"
)

// Start after Stop must be a no-op: it must not spawn a poll loop (which would
// run on the cancelled runner ctx) and must not Add to the WaitGroup after
// Stop's Wait (a reuse panic).
func TestRunnerStartAfterStopIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{}, 1)
		s := sensor.NewFunc("after-stop", time.Hour, func(context.Context) (sensor.Observation, error) {
			pollStarted <- struct{}{}
			return sensor.Observation{}, nil
		})
		r := sensor.New()
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
	s := sensor.NewFunc("race", time.Hour, func(context.Context) (sensor.Observation, error) {
		return sensor.Observation{}, nil
	})
	r := sensor.New()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.Start(t.Context()) }()
	go func() { defer wg.Done(); r.Stop() }()
	wg.Wait()
}

func TestRunnerRepeatedStartDoesNotDuplicatePollers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{}, 2)
		release := make(chan struct{})
		s := sensor.NewFunc("once", time.Hour, func(context.Context) (sensor.Observation, error) {
			pollStarted <- struct{}{}
			<-release
			return sensor.Observation{}, sensor.ErrNoChange
		})
		r := sensor.New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.Start(t.Context())
		r.Start(t.Context())
		<-pollStarted
		select {
		case <-pollStarted:
			t.Fatal("second Start launched a duplicate poller")
		default:
		}
		close(release)
		r.Stop()
	})
}

func TestRunnerStopCancelsContextAwarePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pollStarted := make(chan struct{})
		pollCanceled := make(chan struct{})
		s := sensor.NewFunc("blocking", 100*time.Millisecond, func(ctx context.Context) (sensor.Observation, error) {
			pollStarted <- struct{}{}
			<-ctx.Done()
			close(pollCanceled)
			return sensor.Observation{}, ctx.Err()
		})

		r := sensor.New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.Start(t.Context())
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
		pollStarted := make(chan struct{})
		releasePoll := make(chan struct{})
		handlerCalled := make(chan struct{}, 1)
		pollDone := make(chan struct{})
		s := sensor.NewFunc("stubborn", 100*time.Millisecond, func(context.Context) (sensor.Observation, error) {
			pollStarted <- struct{}{}
			<-releasePoll
			close(pollDone)
			return sensor.Observation{Value: "late"}, nil
		})

		r := sensor.New()
		if err := r.Register(s); err != nil {
			t.Fatalf("Register: %v", err)
		}
		r.OnChange(func(context.Context, string, sensor.Observation) { handlerCalled <- struct{}{} })
		r.Start(t.Context())
		<-pollStarted

		r.Stop()
		close(releasePoll)
		<-pollDone
		synctest.Wait()
		select {
		case <-handlerCalled:
			t.Fatal("handler fired after Stop returned")
		default:
		}
	})
}
