package pursue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSupervisorRetainsLateSettlementAndJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := pursue.NewSupervisor()
		release := make(chan struct{})
		firstOutput := 2 * time.Second
		out := owner.Drive(t.Context(), pursue.NewRequest(func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
			<-ctx.Done()
			<-release
			return runner.TaskResult{
				ID: "late", Reason: runner.TerminalCancelled, Err: ctx.Err(),
				Messages:   []llm.Message{{Role: llm.RoleAssistant, Content: "retained"}},
				Timing:     runner.TaskTiming{TimeToFirstOutput: &firstOutput},
				TotalUsage: &llm.Usage{TotalTokens: 31},
			}
		}, runner.TaskSpec{ID: "late"}), pursue.WithAttemptTimeout(time.Second), pursue.WithCancelDrainTimeout(time.Second))
		if !errors.Is(out.Result.Err, pursue.ErrAttemptTimeout) {
			t.Fatalf("outcome=%+v", out)
		}
		if _, settled := owner.LastResult(); settled {
			t.Fatal("unsettled result presented as final")
		}
		bounded, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := owner.Join(bounded); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Join=%v", err)
		}
		close(release)
		if err := owner.Join(t.Context()); err != nil {
			t.Fatal(err)
		}
		result, settled := owner.LastResult()
		if !settled || result.ID != "late" || result.TotalUsage.TotalTokens != 31 {
			t.Fatalf("late=%+v settled=%v", result, settled)
		}
		result.Messages[0].Content = "mutated"
		result.TotalUsage.TotalTokens = 0
		*result.Timing.TimeToFirstOutput = 0
		again, _ := owner.LastResult()
		if again.Messages[0].Content != "retained" || again.TotalUsage.TotalTokens != 31 || *again.Timing.TimeToFirstOutput != 2*time.Second {
			t.Fatal("result aliases owner storage")
		}
		reused := owner.Drive(t.Context(), pursue.NewRequest(func(context.Context, runner.TaskSpec) runner.TaskResult {
			t.Fatal("owner reused")
			return runner.TaskResult{}
		}, runner.TaskSpec{}))
		if !errors.Is(reused.Result.Err, pursue.ErrSupervisorClosed) {
			t.Fatalf("result error=%v, want ErrSupervisorClosed", reused.Result.Err)
		}
		if !errors.Is(reused.Err(), pursue.ErrSupervisorClosed) {
			t.Fatalf("outcome error=%v, want ErrSupervisorClosed", reused.Err())
		}
		if got := reused.Status(); got != pursue.Statuses.ERRORED {
			t.Fatalf("status=%v, want %v", got, pursue.Statuses.ERRORED)
		}
	})
}

func TestSupervisorJoinsOwnedPollingProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := pursue.NewSupervisor()
		started := make(chan struct{})
		release := make(chan struct{})
		watcher := owner.PollWatcher(func(ctx context.Context) bool { close(started); <-ctx.Done(); <-release; return false }, time.Second)
		out := owner.Drive(t.Context(), pursue.NewRequest(func(context.Context, runner.TaskSpec) runner.TaskResult {
			<-started
			return runner.TaskResult{Reason: runner.TerminalCompleted}
		}, runner.TaskSpec{}, pursue.WithWatcher(watcher)))
		if out.Result.Reason != runner.TerminalCompleted {
			t.Fatalf("outcome=%+v", out)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := owner.Join(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("probe abandoned: %v", err)
		}
		close(release)
		if err := owner.Join(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSupervisorStopsPollingProbeOutsideDrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := pursue.NewSupervisor()
		started := make(chan struct{})
		watcher := owner.PollWatcher(func(ctx context.Context) bool {
			close(started)
			<-ctx.Done()
			return false
		}, time.Hour)

		watcher(t.Context())
		<-started
		owner.Stop()
		if err := owner.Join(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSupervisorSerializesPollingProbeInvocations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := pursue.NewSupervisor()
		watcherCtx, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		secondCtx, cancelSecond := context.WithCancel(t.Context())
		defer cancelSecond()

		entered := make(chan struct{}, 2)
		concurrent := make(chan struct{}, 1)
		var running atomic.Int32
		watcher := owner.PollWatcher(func(ctx context.Context) bool {
			if running.Add(1) != 1 {
				concurrent <- struct{}{}
			}
			defer running.Add(-1)
			entered <- struct{}{}
			<-ctx.Done()
			return false
		}, time.Hour)

		watcher(watcherCtx)
		<-entered
		watcher(secondCtx)
		synctest.Wait()
		select {
		case <-entered:
			t.Fatal("overlapping watchers invoked probe concurrently")
		default:
		}

		cancelFirst()
		<-entered
		select {
		case <-concurrent:
			t.Fatal("probe ran concurrently")
		default:
		}

		owner.Stop()
		if err := owner.Join(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSupervisorCanceledPollingWaiterDoesNotProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := pursue.NewSupervisor()
		firstCtx, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		waiterCtx, cancelWaiter := context.WithCancel(t.Context())
		defer cancelWaiter()

		entered := make(chan struct{}, 2)
		watcher := owner.PollWatcher(func(ctx context.Context) bool {
			entered <- struct{}{}
			<-ctx.Done()
			return false
		}, time.Hour)

		watcher(firstCtx)
		<-entered
		watcher(waiterCtx)
		synctest.Wait()
		cancelWaiter()
		cancelFirst()
		synctest.Wait()
		select {
		case <-entered:
			t.Fatal("canceled waiter invoked probe")
		default:
		}

		owner.Stop()
		if err := owner.Join(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
