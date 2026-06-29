package pursue_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// completed is a clean terminal result carrying one assistant message,
// so re-drive threading (Context = prior Messages) is observable.
func completed(msg string) runner.TaskResult {
	return runner.TaskResult{
		Reason:       runner.TerminalCompleted,
		FinalContent: msg,
		Messages:     []llm.Message{{Role: "assistant", Content: msg}},
	}
}

func TestDrive_SucceedsFirstAttempt(t *testing.T) {
	calls := 0
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		calls++
		return completed("done")
	}
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{Prompt: "go"}, pursue.WithGoal(pursue.AcceptCompleted())))
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Attempts != 1 || calls != 1 {
		t.Fatalf("status=%v attempts=%d calls=%d; want succeeded/1/1", out.Status(), out.Attempts, calls)
	}
	if out.Verified {
		t.Fatal("AcceptCompleted success should be trusted, not verified")
	}
}

func TestDrive_RedrivesUntilGoalMet(t *testing.T) {
	var seen []runner.TaskSpec
	run := func(_ context.Context, spec runner.TaskSpec) runner.TaskResult {
		seen = append(seen, spec)
		return completed(fmt.Sprintf("attempt-%d", len(seen)))
	}
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if attempt.Number >= 3 {
			return pursue.Done()
		}
		return pursue.Retry(fmt.Sprintf("not yet, retry #%d", attempt.Number))
	})

	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{Prompt: "go"}, pursue.WithGoal(goal)), pursue.WithMaxAttempts(5), pursue.WithContextThreader(pursue.ThreadFullTranscript()))
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Attempts != 3 {
		t.Fatalf("status=%v attempts=%d; want succeeded/3", out.Status(), out.Attempts)
	}
	if out.Verified {
		t.Fatal("a bare GoalFunc must not be reported verified — it may not check the world; wrap with pursue.Verified to opt in")
	}
	// First attempt sees the original prompt and no prior context.
	if seen[0].Prompt != "go" || len(seen[0].Context) != 0 {
		t.Fatalf("attempt 1 spec = %+v; want prompt=go, empty context", seen[0])
	}
	// Re-driven attempts carry the prior conversation as Context and the
	// goal's feedback as the new Prompt.
	if seen[1].Prompt != "not yet, retry #1" || len(seen[1].Context) == 0 {
		t.Fatalf("attempt 2 spec = %+v; want feedback prompt + threaded context", seen[1])
	}
	if seen[2].Prompt != "not yet, retry #2" {
		t.Fatalf("attempt 3 prompt = %q; want feedback #2", seen[2].Prompt)
	}
}

func TestDrive_DefaultThreaderDropsContext(t *testing.T) {
	var seen []runner.TaskSpec
	run := func(_ context.Context, spec runner.TaskSpec) runner.TaskResult {
		seen = append(seen, spec)
		return completed(fmt.Sprintf("attempt-%d", len(seen)))
	}
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if attempt.Number >= 2 {
			return pursue.Done()
		}
		return pursue.Retry("try again")
	})

	// No WithContextThreader: the conservative ThreadNoContext default
	// applies, so a re-driven attempt carries only the feedback prompt and
	// no prior messages — bounding context growth across retries.
	out := pursue.Drive(t.Context(),
		pursue.NewRequest(run, runner.TaskSpec{Prompt: "go"}, pursue.WithGoal(goal)),
		pursue.WithMaxAttempts(3),
	)
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Attempts != 2 {
		t.Fatalf("status=%v attempts=%d; want succeeded/2", out.Status(), out.Attempts)
	}
	if seen[1].Prompt != "try again" {
		t.Errorf("attempt 2 prompt = %q; want the feedback %q", seen[1].Prompt, "try again")
	}
	if len(seen[1].Context) != 0 {
		t.Errorf("attempt 2 context = %d msgs; want none under the no-context default", len(seen[1].Context))
	}
}

func TestDrive_VerifiedWrapperOptsGoalIntoVerified(t *testing.T) {
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return completed("done")
	}
	base := pursue.GoalFunc(func(context.Context, pursue.Attempt) pursue.Decision {
		return pursue.Done()
	})

	// Bare GoalFunc: not verified.
	plain := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(base)))
	if plain.Verified {
		t.Fatal("bare GoalFunc should report Verified == false")
	}

	// Same goal wrapped with Verified: reported verified on success.
	wrapped := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(pursue.Verified(base))))
	if wrapped.Status() != pursue.Statuses.SUCCEEDED || !wrapped.Verified {
		t.Fatalf("pursue.Verified(goal) success must report Verified; status=%v verified=%v", wrapped.Status(), wrapped.Verified)
	}
}

func TestDrive_GivesUpAtBudget(t *testing.T) {
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return completed("x")
	}
	goal := pursue.GoalFunc(func(_ context.Context, _ pursue.Attempt) pursue.Decision {
		return pursue.Retry("never satisfied")
	})
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal)), pursue.WithMaxAttempts(2))
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.GAVEUP || out.Attempts != 2 {
		t.Fatalf("status=%v attempts=%d; want gave_up/2", out.Status(), out.Attempts)
	}
}

func TestDrive_ErroredSurfacesAndStops(t *testing.T) {
	want := errors.New("boom")
	calls := 0
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		calls++
		return runner.TaskResult{Reason: runner.TerminalError, Err: want}
	}
	var reports []pursue.AttemptReport
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(pursue.AcceptCompleted())), pursue.WithMaxAttempts(3), pursue.WithOnAttempt(func(r pursue.AttemptReport) {
		reports = append(reports, r)
	}))
	if !errors.Is(out.Err(), want) {
		t.Fatalf("err = %v; want %v", out.Err(), want)
	}
	if out.Status() != pursue.Statuses.ERRORED || calls != 1 {
		t.Fatalf("status=%v calls=%d; want errored/1 (no re-drive on error)", out.Status(), calls)
	}
	if len(reports) != 1 || reports[0].Decision.Done {
		t.Fatalf("reports = %+v; want one non-done error report", reports)
	}
}

func TestDrive_TerminalErrorResultSurfacesAsErrored(t *testing.T) {
	want := errors.New("runner terminal error")
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Reason: runner.TerminalError, Err: want}
	}
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}), pursue.WithMaxAttempts(3))
	if !errors.Is(out.Err(), want) {
		t.Fatalf("err = %v; want %v", out.Err(), want)
	}
	if out.Status() != pursue.Statuses.ERRORED || out.Attempts != 1 {
		t.Fatalf("status=%v attempts=%d; want errored/1", out.Status(), out.Attempts)
	}
}

func TestDrive_NilGoalAcceptsCompleted(t *testing.T) {
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return completed("ok")
	}
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}))
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("status=%v; want succeeded (nil Goal ⇒ AcceptCompleted)", out.Status())
	}
}

func TestDrive_NilGoalGivesUpOnNonCompleted(t *testing.T) {
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Reason: runner.TerminalMaxIterations}
	}
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}))
	if out.Status() != pursue.Statuses.GAVEUP || out.Result.Reason != runner.TerminalMaxIterations {
		t.Fatalf("status=%v reason=%v; want gave_up/max_iterations", out.Status(), out.Result.Reason)
	}
}

func TestDrive_OnAttemptFiresPerAttemptWithDecision(t *testing.T) {
	var got []pursue.AttemptReport
	run := func(_ context.Context, _ runner.TaskSpec) runner.TaskResult {
		return completed("x")
	}
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if attempt.Number == 3 {
			return pursue.Done()
		}
		return pursue.Retry("again")
	})
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal)),
		pursue.WithMaxAttempts(3),
		pursue.WithOnAttempt(func(r pursue.AttemptReport) { got = append(got, r) }),
	)
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if len(got) != 3 || got[0].Attempt.Number != 1 || got[2].Attempt.Number != 3 {
		t.Fatalf("onAttempt calls = %v; want attempts [1 2 3]", got)
	}
	if got[0].Decision.Done || !got[2].Decision.Done {
		t.Fatalf("decisions = %+v; want retry, retry, done", got)
	}
	if label := pursue.LabelAttempt(got[2]); label != "goal met" {
		t.Fatalf("label = %q; want goal met", label)
	}
}

func TestDrive_UntilFuncEarlyStopCancelsAttemptAndSucceeds(t *testing.T) {
	var goalMet atomic.Bool
	var report pursue.AttemptReport
	run := func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
		goalMet.Store(true)
		<-ctx.Done()
		return runner.TaskResult{Reason: runner.TerminalCancelled}
	}
	goal, watcher := pursue.UntilFunc(goalMet.Load, func(context.Context, pursue.Attempt) string {
		return "goal intentionally ignored; UntilFunc owns this path"
	})

	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithMaxAttempts(3),
		pursue.WithOnAttempt(func(r pursue.AttemptReport) { report = r }),
	)
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Attempts != 1 || out.Result.Reason != runner.TerminalCancelled {
		t.Fatalf("out = %+v; want succeeded/1/cancelled", out)
	}
	if !report.Decision.Done {
		t.Fatalf("report = %+v; want done decision", report)
	}
	if !out.Verified {
		t.Fatal("UntilFunc success should be marked verified")
	}
}

func TestDrive_WatcherDoesNotOverrideGoal(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	watcher := func(context.Context) <-chan struct{} { return ch }
	run := func(context.Context, runner.TaskSpec) runner.TaskResult {
		return completed("attempt finished")
	}
	goal := pursue.GoalFunc(func(context.Context, pursue.Attempt) pursue.Decision {
		return pursue.Retry("watcher fired but goal disagrees")
	})

	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)))
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.GAVEUP {
		t.Fatalf("status=%v; want gave_up because Goal did not verify", out.Status())
	}
	if out.Verified {
		t.Fatal("gave-up outcome must not be verified")
	}
}

func TestDrive_WatcherTimeoutsWhenAttemptDoesNotDrain(t *testing.T) {
	var goalMet atomic.Bool
	release := make(chan struct{})
	defer close(release)
	run := func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
		goalMet.Store(true)
		<-ctx.Done()
		<-release
		return runner.TaskResult{Reason: runner.TerminalCancelled}
	}
	goal, watcher := pursue.Until(goalMet.Load, "not done")

	start := time.Now()
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithCancelDrainTimeout(10*time.Millisecond),
	)
	if !errors.Is(out.Err(), pursue.ErrAttemptCancelDrainTimeout) {
		t.Fatalf("err = %v; want ErrAttemptCancelDrainTimeout", out.Err())
	}
	if out.Status() != pursue.Statuses.ERRORED {
		t.Fatalf("status=%v; want errored", out.Status())
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Drive took %s; want quick timeout after watcher cancellation", elapsed)
	}
}

func TestDrive_NoWatcherWaitsForNaturalCompletion(t *testing.T) {
	var goalMet atomic.Bool
	run := func(context.Context, runner.TaskSpec) runner.TaskResult {
		goalMet.Store(true)
		return completed("done after model stop")
	}
	goal, _ := pursue.Until(goalMet.Load, "not done")

	// No WithWatcher — Drive runs the attempt to natural completion and
	// only then consults the Goal. Result.Reason stays TerminalCompleted.
	out := pursue.Drive(t.Context(), pursue.NewRequest(run, runner.TaskSpec{}, pursue.WithGoal(goal)))
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Result.Reason != runner.TerminalCompleted {
		t.Fatalf("out = %+v; want succeeded after normal completion", out)
	}
}

func TestDrive_NilAttemptFuncErrors(t *testing.T) {
	out := pursue.Drive(t.Context(), pursue.NewRequest(nil, runner.TaskSpec{}))
	if out.Err() == nil {
		t.Fatal("err = nil; want nil AttemptFunc error")
	}
	if out.Status() != pursue.Statuses.ERRORED {
		t.Fatalf("status=%v; want errored", out.Status())
	}
}

func TestDrive_RetryEmptyFeedbackKeepsPrompt(t *testing.T) {
	var seen []runner.TaskSpec
	run := func(_ context.Context, spec runner.TaskSpec) runner.TaskResult {
		seen = append(seen, spec)
		return completed(fmt.Sprintf("attempt-%d", len(seen)))
	}
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if attempt.Number >= 2 {
			return pursue.Done()
		}
		return pursue.Retry("")
	})

	out := pursue.Drive(t.Context(),
		pursue.NewRequest(run, runner.TaskSpec{Prompt: "original"}, pursue.WithGoal(goal)),
		pursue.WithMaxAttempts(2),
		pursue.WithContextThreader(pursue.ThreadFullTranscript()),
	)
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || out.Attempts != 2 {
		t.Fatalf("status=%v attempts=%d; want succeeded/2", out.Status(), out.Attempts)
	}
	if seen[1].Prompt != "original" {
		t.Fatalf("attempt 2 prompt = %q; want unchanged %q", seen[1].Prompt, "original")
	}
	if len(seen[1].Context) == 0 {
		t.Fatalf("attempt 2 context empty; want threaded prior conversation")
	}
}

func TestDrive_ContextThreaderOverridesRedriveSpec(t *testing.T) {
	var seen []runner.TaskSpec
	run := func(_ context.Context, spec runner.TaskSpec) runner.TaskResult {
		seen = append(seen, spec)
		return completed(fmt.Sprintf("attempt-%d", len(seen)))
	}
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		if attempt.Number >= 2 {
			return pursue.Done()
		}
		return pursue.Retry("default feedback should be replaced")
	})
	threader := func(_ context.Context, attempt pursue.Attempt, next runner.TaskSpec, decision pursue.Decision) runner.TaskSpec {
		if attempt.Number != 1 || decision.Feedback == "" {
			t.Fatalf("threader saw attempt=%d decision=%+v", attempt.Number, decision)
		}
		next.Prompt = "threaded prompt"
		next.Context = []llm.Message{{Role: llm.RoleUser, Content: "summary only"}}
		return next
	}

	out := pursue.Drive(t.Context(),
		pursue.NewRequest(run, runner.TaskSpec{Prompt: "original"}, pursue.WithGoal(goal)),
		pursue.WithMaxAttempts(2),
		pursue.WithContextThreader(threader),
	)
	if out.Err() != nil {
		t.Fatalf("err: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED || len(seen) != 2 {
		t.Fatalf("status=%v seen=%d; want succeeded with two attempts", out.Status(), len(seen))
	}
	if seen[1].Prompt != "threaded prompt" {
		t.Fatalf("attempt 2 prompt = %q; want threaded prompt", seen[1].Prompt)
	}
	if len(seen[1].Context) != 1 || seen[1].Context[0].Content != "summary only" {
		t.Fatalf("attempt 2 context = %+v; want threader-provided summary", seen[1].Context)
	}
}
