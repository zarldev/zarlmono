package tui_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestManualCompactionTUIShutdownJoinsCanceledProviderAfterDeadline(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	f := manualTUIFixtureWithApplications(t, func(ctx context.Context) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
		}
	}, true)
	appCtx, cancelApp := context.WithCancel(t.Context())
	t.Cleanup(cancelApp)
	f.ui.SetApplicationContext(appCtx)
	command := f.ui.CompactNow()
	var workers sync.WaitGroup
	workers.Go(func() { _ = command() })
	var releaseOnce sync.Once
	testCtx := t.Context()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), 5*time.Second)
		defer cancel()
		if err := f.ui.FlushSessionPersistence(ctx); err != nil {
			t.Error(err)
		}
		workers.Wait()
	})
	<-entered
	cancelApp()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.ui.FlushSessionPersistence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled flush = %v", err)
	}
	<-canceled
	if reservation, err := f.live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
		if err == nil {
			reservation.Release()
		}
		t.Fatalf("timed-out flush abandoned active compaction: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	retryCtx, retryCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer retryCancel()
	if err := f.ui.FlushSessionPersistence(retryCtx); err != nil {
		t.Fatalf("resumed shutdown = %v", err)
	}
	workers.Wait()
	assertManualSavedAndReleased(t, f, 1, int(calls.Load()))
}
