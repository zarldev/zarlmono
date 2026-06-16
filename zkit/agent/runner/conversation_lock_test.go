package runner_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestConversationLock_AcquireReleaseIsActive(t *testing.T) {
	t.Parallel()

	lock := runner.NewConversationLock()
	if lock.IsActive() {
		t.Fatal("new lock should not be active")
	}

	lock.Acquire()
	if !lock.IsActive() {
		t.Error("after Acquire, IsActive should be true")
	}

	lock.Release()
	if lock.IsActive() {
		t.Error("after Release, IsActive should be false")
	}

	// Multiple acquires are idempotent (no deadlock, still active).
	lock.Acquire()
	lock.Acquire()
	if !lock.IsActive() {
		t.Error("double-Acquire should still be active")
	}
	lock.Release()
}

func TestConversationLock_WaitReturnsImmediatelyWhenInactive(t *testing.T) {
	t.Parallel()

	lock := runner.NewConversationLock()
	// No goroutines, no time involved — Wait sees !active and returns nil.
	if err := lock.Wait(context.Background()); err != nil {
		t.Errorf("Wait on inactive lock returned error: %v", err)
	}
}

func TestConversationLock_WaitBlocksUntilRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lock := runner.NewConversationLock()
		lock.Acquire()

		done := make(chan error, 1)
		go func() {
			done <- lock.Wait(t.Context())
		}()

		// synctest.Wait blocks until every goroutine in the bubble
		// is blocked. After it returns, the waiter goroutine MUST
		// be inside lock.Wait — anything else is a regression.
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("Wait returned early with %v", err)
		default:
		}

		lock.Release()

		// After Release, the waiter wakes and returns. synctest.Wait
		// blocks until the goroutine is done.
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Wait returned error after Release: %v", err)
			}
		default:
			t.Fatal("Wait did not return after Release")
		}
	})
}

func TestConversationLock_WaitRespectsContextCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lock := runner.NewConversationLock()
		lock.Acquire()
		t.Cleanup(lock.Release)

		// Synthetic clock — DeadlineExceeded fires deterministically
		// once no goroutine in the bubble is runnable.
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		t.Cleanup(cancel)

		err := lock.Wait(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
	})
}

func TestConversationLock_WakesAllWaitersOnRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lock := runner.NewConversationLock()
		lock.Acquire()

		const n = 8
		var woken atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				if err := lock.Wait(t.Context()); err != nil {
					t.Errorf("waiter Wait err: %v", err)
					return
				}
				woken.Add(1)
			}()
		}

		// All n goroutines block in Wait — synctest.Wait blocks until
		// every goroutine in the bubble is blocked.
		synctest.Wait()
		if woken.Load() != 0 {
			t.Fatalf("waiters released before Release was called: %d woken", woken.Load())
		}

		lock.Release()
		wg.Wait()

		if got := woken.Load(); got != n {
			t.Errorf("woken = %d, want %d", got, n)
		}
	})
}
