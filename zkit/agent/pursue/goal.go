package pursue

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// Goal is the programmatic completion oracle. It inspects a finished
// attempt and reports whether the task's goal is met. When it isn't,
// Feedback is the corrective message the next attempt is re-driven with
// — appended as the next user turn after the prior conversation.
//
// A Goal should verify the world (did the change actually take effect?)
// rather than trust the model's self-reported completion; AcceptCompleted
// is the trivial exception for single-attempt runs.
type Goal interface {
	Evaluate(context.Context, Attempt) Decision
}

// verifiedGoal marks Goals that verify world state — a real predicate or
// external check — rather than trusting the agent/runner's self-reported
// terminal condition. Drive uses this marker only to populate
// Outcome.Verified; all Goal evaluation semantics are unchanged.
//
// Verification is opt-in, not inferred: Until / UntilFunc are verified by
// construction, AcceptCompleted is not, and a bare GoalFunc is not unless
// wrapped with Verified — the harness can't tell whether an arbitrary
// GoalFunc inspects the world or just rubber-stamps the model, so it
// errs toward not over-claiming.
type verifiedGoal interface {
	Goal
	verifiedGoal()
}

// Verified marks goal as world-verifying so a successful Outcome built
// from it reports Verified == true. Use it to wrap a custom GoalFunc
// oracle that genuinely checks external state (ran the tests, hit the
// endpoint, re-read the file). Until / UntilFunc are already verified, so
// wrapping them is a harmless no-op. A nil goal stays nil.
func Verified(goal Goal) Goal {
	if goal == nil {
		return nil
	}
	if _, ok := goal.(verifiedGoal); ok {
		return goal
	}
	return verifiedWrapper{goal}
}

type verifiedWrapper struct{ Goal }

func (verifiedWrapper) verifiedGoal() {}

// GoalFunc adapts a plain function into a Goal. It inspects a finished
// Attempt and returns a Decision — Done() to terminate, Retry(feedback)
// to re-drive with corrective context.
//
// Reach for GoalFunc when neither AcceptCompleted (trust-the-model) nor
// Until / UntilFunc (predicate-backed verification) fits — for example,
// when the oracle needs to inspect Attempt.Result.Messages or run an
// arbitrary check that doesn't reduce to a simple bool predicate.
type GoalFunc func(context.Context, Attempt) Decision

// Evaluate implements Goal.
func (f GoalFunc) Evaluate(ctx context.Context, attempt Attempt) Decision {
	return f(ctx, attempt)
}

// Watcher signals early stop during an in-flight attempt. The returned channel
// closes once the goal may be met; Drive cancels the in-flight attempt, lets it
// drain, and asks Goal.Evaluate for the final verdict. A nil Watcher on a
// Request disables early stop — Drive then waits for each attempt to complete
// naturally before consulting Goal.Evaluate.
//
// The channel must NOT be closed on context cancellation — only on
// goal-met. A canceled attempt that completes without the goal being
// met should fall through to normal Goal evaluation.
type Watcher func(ctx context.Context) <-chan struct{}

// Done marks a goal as satisfied.
func Done() Decision { return Decision{Done: true} }

// Retry marks a goal as not yet satisfied and carries the corrective
// prompt for the next attempt. Empty feedback means "re-run with the
// same prompt"; only the threaded conversation grows.
func Retry(feedback string) Decision { return Decision{Feedback: feedback} }

// AcceptCompleted is the trivial oracle: it trusts the runner's terminal
// reason. The goal counts as met whenever the attempt terminated cleanly
// (the model stopped on its own). It does NO world verification.
//
// This is the headless default; a nil Goal uses it. For any run where
// wrong-completion has a cost, supply a real Goal that verifies world
// state — Until / UntilFunc are the common path.
func AcceptCompleted() Goal {
	// A plain GoalFunc, deliberately unmarked: trusting the terminal
	// reason is not world verification, so an Outcome built from it
	// reports Verified == false.
	return GoalFunc(func(_ context.Context, attempt Attempt) Decision {
		if attempt.Result.Reason == runner.TerminalCompleted {
			return Done()
		}
		return Retry("")
	})
}

// DefaultPollInterval is the cadence at which Until / UntilFunc poll the
// caller's predicate to detect goal-met during an in-flight attempt.
const DefaultPollInterval = 100 * time.Millisecond

// Until builds a predicate-backed Goal and a polling Watcher from the same
// predicate. The Watcher polls done at DefaultPollInterval while an attempt is
// in flight, closing its channel once done flips to true so Drive can cancel the
// attempt and ask the Goal for a final verdict without waiting for natural
// attempt completion.
//
// Pass both into the Request:
//
//	goal, watcher := pursue.Until(rel.Published, "not yet")
//	pursue.Drive(ctx, pursue.NewRequest(r.Run, spec,
//	    pursue.WithGoal(goal), pursue.WithWatcher(watcher)))
//
// Omit the Watcher to disable early stop — Drive then waits for each
// attempt to complete before consulting the Goal.
func Until(done func() bool, feedback string) (Goal, Watcher) {
	return UntilFunc(done, func(context.Context, Attempt) string { return feedback })
}

// UntilFunc is Until with dynamic feedback derived from the finished
// attempt. Returns both a Goal (post-attempt verdict) and a Watcher
// (early-stop signal during the attempt) built from one shared
// predicate.
func UntilFunc(done func() bool, feedback func(context.Context, Attempt) string) (Goal, Watcher) {
	return untilGoal{done: done, feedback: feedback}, predicateWatcher(done, DefaultPollInterval)
}

type untilGoal struct {
	done     func() bool
	feedback func(context.Context, Attempt) string
}

func (g untilGoal) Evaluate(ctx context.Context, attempt Attempt) Decision {
	if g.done != nil && g.done() {
		return Done()
	}
	if g.feedback == nil {
		return Retry("")
	}
	return Retry(g.feedback(ctx, attempt))
}

// verifiedGoal marks untilGoal as world-verifying: its verdict comes from
// the caller's done() predicate, which polls real state.
func (untilGoal) verifiedGoal() {}

// PollWatcher returns a Watcher that calls probe on a ticker and fires —
// closes its channel — the first time probe returns true. It is the general
// early-stop primitive: a consumer that can cheaply ask "is the goal met?"
// (e.g. run a fast test command) builds a Watcher from it, decoupled from the
// authoritative Goal that runs at the barrier. probe is checked once
// immediately, then every interval.
//
// probe receives the attempt ctx and MUST honour it for its own
// timeout/cancellation — a probe that blocks past attempt cancellation
// stalls Drive's drain. A nil probe never fires (the watcher just lives until
// ctx is done — the "no early stop" case). interval <= 0 uses
// DefaultPollInterval.
//
// On ctx cancellation the goroutine exits WITHOUT closing the channel —
// cancellation is not goal-met. The goroutine is bounded by ctx (Drive
// cancels it when the attempt ends), so there is no fire-and-forget.
func PollWatcher(probe func(ctx context.Context) bool, interval time.Duration) Watcher {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return func(ctx context.Context) <-chan struct{} {
		ch := make(chan struct{})
		go func() {
			if probe == nil {
				<-ctx.Done()
				return
			}
			if probe(ctx) {
				close(ch)
				return
			}
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if probe(ctx) {
						close(ch)
						return
					}
				}
			}
		}()
		return ch
	}
}

// predicateWatcher adapts a side-effect-free done() predicate onto
// PollWatcher. Used by Until / UntilFunc, where the watcher and the Goal
// share one predicate.
func predicateWatcher(done func() bool, interval time.Duration) Watcher {
	var probe func(context.Context) bool
	if done != nil {
		probe = func(context.Context) bool { return done() }
	}
	return PollWatcher(probe, interval)
}
