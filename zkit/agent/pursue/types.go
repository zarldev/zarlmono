package pursue

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// AttemptFunc executes one attempt: drive a single TaskSpec to a terminal
// TaskResult. Every outcome — completed, max iterations, cancelled, or an
// unrecoverable error — is encoded in the TaskResult (Reason + Err); there
// is no separate error channel, so Drive reads one place. The runner's Run
// method (r.Run) satisfies it directly; a consumer can wrap it (e.g. with
// context-overflow recovery) and Drive does not know or care.
type AttemptFunc func(ctx context.Context, spec runner.TaskSpec) runner.TaskResult

// ContextThreader builds the TaskSpec for the next attempt after a Goal returns
// Retry. Drive's default is ThreadNoContext — the next attempt carries no prior
// messages, only non-empty Decision.Feedback as its Prompt — so an unaware
// caller can't balloon the context across retries.
//
// Consumers that want history carried forward install ThreadFullTranscript (the
// whole prior transcript) or a custom threader that keeps only a
// compacted/summarized history or carries verifier feedback without replaying
// the entire previous transcript.
type ContextThreader func(ctx context.Context, attempt Attempt, next runner.TaskSpec, decision Decision) runner.TaskSpec

// Attempt is one finished AttemptFunc call plus the exact TaskSpec it saw.
type Attempt struct {
	Number int
	Spec   runner.TaskSpec
	Result runner.TaskResult
}

// Decision is a Goal's verdict for an attempt. When Done is false and
// more attempts remain, non-empty Feedback becomes the next attempt's
// Prompt. Empty Feedback keeps the prior Prompt — the next attempt
// re-runs with the same instruction but the threaded conversation as
// added context.
type Decision struct {
	Done     bool
	Feedback string
}

// AttemptReport pairs a finished attempt with its pre-computed goal
// Decision. WithOnAttempt hooks receive this so callers never need to
// re-evaluate the goal just for logging or labels.
type AttemptReport struct {
	Attempt  Attempt
	Decision Decision
}

// Outcome is the terminal result of Drive: a classification (Succeeded,
// GaveUp, or Errored), whether a successful result was verified by a real Goal,
// plus the number of attempts and the last attempt's TaskResult.
//
// Status and Err are methods, not fields: the classification and the
// underlying error are derived from one unexported state (err + gaveUp)
// so the invariant "Err non-nil iff Status == Errored" cannot be
// violated. Drive is the only constructor; outside callers receive
// Outcomes by value and inspect them via Status(), Err(), Result, and
// Attempts.
type Outcome struct {
	Attempts int
	Result   runner.TaskResult
	// Verified is true only when Status() is SUCCEEDED and the satisfying
	// Goal was explicitly marked world-verifying — Until / UntilFunc, or a
	// Goal wrapped with Verified. It is false for AcceptCompleted (which
	// only trusts the runner's TerminalCompleted reason) and for a bare
	// GoalFunc (which the harness can't assume inspects the world). The
	// marker is opt-in so Verified never over-claims.
	Verified bool
	err      error
	gaveUp   bool
}

// Status returns the terminal classification. Errored takes precedence
// over GaveUp.
func (o Outcome) Status() Status {
	switch {
	case o.err != nil:
		return Statuses.ERRORED
	case o.gaveUp:
		return Statuses.GAVEUP
	default:
		return Statuses.SUCCEEDED
	}
}

// Err returns the underlying error when Status is Errored, nil otherwise.
func (o Outcome) Err() error {
	return o.err
}
