package pursue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// A wedged AttemptFunc that ignores ctx must not hang Drive forever:
// WithAttemptTimeout bounds it and surfaces an ERRORED Outcome carrying
// ErrAttemptTimeout. Without the timeout this AttemptFunc would block
// until the test's own deadline.
func TestDrive_AttemptTimeoutBoundsWedgedAttempt(t *testing.T) {
	t.Parallel()
	released := make(chan struct{})
	t.Cleanup(func() { close(released) }) // let the leaked goroutine exit at test end

	wedged := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		<-released // deliberately ignores ctx — simulates a stuck tool/provider
		return runner.TaskResult{Reason: runner.TerminalCompleted}
	}

	req := pursue.NewRequest(wedged, runner.TaskSpec{Prompt: "go"})

	done := make(chan pursue.Outcome, 1)
	go func() {
		done <- pursue.Drive(t.Context(), req,
			pursue.WithAttemptTimeout(50*time.Millisecond),
			// Keep the post-cancel drain short so the bounded-but-undrainable
			// attempt resolves quickly to the timeout verdict.
			pursue.WithCancelDrainTimeout(50*time.Millisecond),
		)
	}()

	select {
	case out := <-done:
		if out.Status() != pursue.Statuses.ERRORED {
			t.Fatalf("Status = %v, want ERRORED", out.Status())
		}
		if !errors.Is(out.Err(), pursue.ErrAttemptTimeout) {
			t.Fatalf("Err = %v, want ErrAttemptTimeout", out.Err())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Drive hung on a wedged attempt despite WithAttemptTimeout")
	}
}

// A cooperative attempt that finishes within the budget is unaffected by
// the timeout — it returns its real result.
func TestDrive_AttemptTimeoutLetsFastAttemptThrough(t *testing.T) {
	t.Parallel()
	fast := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Reason: runner.TerminalCompleted, FinalContent: "ok"}
	}
	out := pursue.Drive(t.Context(),
		pursue.NewRequest(fast, runner.TaskSpec{Prompt: "go"}),
		pursue.WithAttemptTimeout(2*time.Second),
	)
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("Status = %v, want SUCCEEDED", out.Status())
	}
	if out.Err() != nil {
		t.Fatalf("Err = %v, want nil", out.Err())
	}
	if out.Result.FinalContent != "ok" {
		t.Errorf("FinalContent = %q, want \"ok\" (the real result must pass through)", out.Result.FinalContent)
	}
}

// A cooperative attempt that DOES honour ctx cancellation still resolves
// promptly under the timeout (drains its own result rather than leaking).
func TestDrive_AttemptTimeoutCancelsCooperativeAttempt(t *testing.T) {
	t.Parallel()
	cooperative := func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
		<-ctx.Done() // honours cancellation
		return runner.TaskResult{Reason: runner.TerminalError, Err: ctx.Err()}
	}
	start := time.Now()
	out := pursue.Drive(t.Context(),
		pursue.NewRequest(cooperative, runner.TaskSpec{Prompt: "go"}),
		pursue.WithAttemptTimeout(50*time.Millisecond),
	)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Drive took %s, want it bounded near the 50ms attempt timeout", elapsed)
	}
	if out.Status() != pursue.Statuses.ERRORED {
		t.Fatalf("Status = %v, want ERRORED", out.Status())
	}
	if !errors.Is(out.Err(), pursue.ErrAttemptTimeout) {
		t.Fatalf("Err = %v, want ErrAttemptTimeout", out.Err())
	}
}
