package pursue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// A cancelled parent ctx makes the runner return TerminalCancelled with Err
// set. Drive must surface that as ERRORED on the first attempt, not burn the
// remaining budget on instantly-cancelled re-drives and report GAVEUP.
func TestDrive_ParentCancellationSurfacesAsErrored(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // parent already cancelled

	calls := 0
	attempt := func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
		calls++
		return runner.TaskResult{Reason: runner.TerminalCancelled, Err: ctx.Err()}
	}

	out := pursue.Drive(ctx,
		pursue.NewRequest(attempt, runner.TaskSpec{Prompt: "go"}),
		pursue.WithMaxAttempts(5),
	)

	if got, want := out.Status(), pursue.Statuses.ERRORED; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if !errors.Is(out.Err(), context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", out.Err())
	}
	if calls != 1 {
		t.Fatalf("attempts = %d, want 1 (no re-drive after cancellation)", calls)
	}
}

// A Watcher-initiated cancellation is Drive's own doing: the cancelled
// attempt still goes to the Goal, and an unmet goal re-drives rather than
// erroring out — the spurious-watcher shape must keep working.
func TestDrive_WatchedCancellationStillRedrives(t *testing.T) {
	calls := 0
	attempt := func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
		calls++
		if calls == 1 {
			// Simulate the runner unwinding after Drive cancels the
			// watched attempt.
			<-ctx.Done()
			return runner.TaskResult{Reason: runner.TerminalCancelled, Err: ctx.Err()}
		}
		return completed("done on retry")
	}

	fired := false
	watcher := pursue.Watcher(func(context.Context) <-chan struct{} {
		ch := make(chan struct{})
		if !fired {
			fired = true
			close(ch) // fire immediately on the first attempt only
		}
		return ch
	})
	goal := pursue.GoalFunc(func(_ context.Context, a pursue.Attempt) pursue.Decision {
		if a.Result.Reason == runner.TerminalCompleted {
			return pursue.Done()
		}
		return pursue.Retry("watcher was premature — keep going")
	})

	out := pursue.Drive(t.Context(),
		pursue.NewRequest(attempt, runner.TaskSpec{Prompt: "go"},
			pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithMaxAttempts(3),
		pursue.WithCancelDrainTimeout(2*time.Second),
	)

	if got, want := out.Status(), pursue.Statuses.SUCCEEDED; got != want {
		t.Fatalf("status = %v (err=%v), want %v", got, out.Err(), want)
	}
	if out.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (cancelled first, completed second)", out.Attempts)
	}
}
