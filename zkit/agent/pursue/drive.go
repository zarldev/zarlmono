package pursue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrAttemptCancelDrainTimeout is returned when a Watcher reports the goal met,
// Drive cancels the in-flight attempt, and the attempt does not return within
// the configured drain grace. It indicates the AttemptFunc is not honoring
// context cancellation promptly enough for verified early-stop mode.
var ErrAttemptCancelDrainTimeout = errors.New("pursue: attempt cancel drain timeout")

// ErrAttemptTimeout is the error set on an attempt's TaskResult when the
// per-attempt deadline (WithAttemptTimeout) fires before the AttemptFunc
// returns. It bounds a wedged attempt — an AttemptFunc that ignores
// ctx.Done(), an infinite tool loop, a dead provider — that would
// otherwise hang Drive forever.
var ErrAttemptTimeout = errors.New("pursue: attempt timeout")

const defaultCancelDrainTimeout = 5 * time.Second

// Config holds Drive options. Populate it only via the WithX options.
type Config struct {
	maxAttempts        int
	onAttempt          func(AttemptReport)
	cancelDrainTimeout time.Duration
	attemptTimeout     time.Duration
	contextThreader    ContextThreader
}

// WithMaxAttempts caps how many attempts Drive will make toward the
// goal. Values < 1 are treated as 1. The default is 1 — a single
// attempt with no re-drive, i.e. the headless shape.
func WithMaxAttempts(n int) options.Option[Config] {
	return func(c *Config) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// WithOnAttempt installs a hook called once per attempt with the
// attempt and its already-computed decision — the seam for per-attempt
// recording / observability (e.g. a run recorder).
func WithOnAttempt(fn func(AttemptReport)) options.Option[Config] {
	return func(c *Config) { c.onAttempt = fn }
}

// WithCancelDrainTimeout sets how long Drive waits for an in-flight attempt to
// return after a Watcher fires and Drive cancels the attempt context. The
// default is 5s. Values <= 0 are ignored. Drive always drains — deterministic
// shutdown beats shaving latency, and Goal.Evaluate must see a settled
// Attempt.Result, not one a still-running goroutine is mutating.
func WithCancelDrainTimeout(d time.Duration) options.Option[Config] {
	return func(c *Config) {
		if d > 0 {
			c.cancelDrainTimeout = d
		}
	}
}

// WithAttemptTimeout bounds how long a single attempt may run before
// Drive abandons it. When the deadline fires, Drive cancels the attempt
// context and — whether or not the AttemptFunc drains — records a
// terminal result with ErrAttemptTimeout, so a wedged attempt surfaces
// as an ERRORED Outcome instead of hanging the caller forever. The
// default is 0, which disables the per-attempt deadline (an attempt runs
// to natural completion, the historical behaviour). Values <= 0 are
// ignored. This is independent of WithCancelDrainTimeout, which only
// governs the post-cancellation drain window once a Watcher fires.
func WithAttemptTimeout(d time.Duration) options.Option[Config] {
	return func(c *Config) {
		if d > 0 {
			c.attemptTimeout = d
		}
	}
}

// WithContextThreader installs the policy that builds the next attempt's
// TaskSpec after a retry decision. The default is ThreadNoContext (re-drive
// with only the Decision's feedback, no prior messages) — conservative so an
// unaware caller can't balloon the context across retries. Use
// ThreadFullTranscript for the prior-conversation-as-context behavior, or
// supply a custom threader (e.g. SWE-bench's summary threader). Pass nil to
// keep the default.
func WithContextThreader(threader ContextThreader) options.Option[Config] {
	return func(c *Config) {
		if threader != nil {
			c.contextThreader = threader
		}
	}
}

// Drive drives req.Attempt toward req.Goal. Each attempt is one
// AttemptFunc call. When the goal isn't met and budget remains, the
// ContextThreader builds the next attempt's TaskSpec from the decision —
// by default (ThreadNoContext) the Decision's feedback becomes the next
// Prompt with no prior messages threaded; install ThreadFullTranscript or
// a custom threader via WithContextThreader to carry history forward. A
// nil req.Goal uses AcceptCompleted.
//
// If req.Watcher is set, Drive cancels an in-flight attempt the moment
// the Watcher's channel closes (goal may be met mid-attempt), waits briefly
// for the attempt to drain, then still asks req.Goal for the final verdict.
// With no Watcher, each attempt runs to natural completion.
//
// Drive returns Statuses.ERRORED with the underlying error on Outcome.Err
// the first time an attempt returns a TaskResult with Err set and a
// Reason of TerminalError or TerminalCancelled (the AttemptFunc has no
// separate error channel — every outcome is in the TaskResult), or when a
// watched attempt fails to drain after cancellation. A cancelled parent
// context therefore surfaces as ERRORED instead of burning the remaining
// attempt budget on instantly-cancelled re-drives and reporting GAVEUP.
// The exception is a cancellation Drive itself initiated (the Watcher
// fired): that attempt still goes to the Goal, and an unmet goal re-drives
// as usual. Otherwise Drive returns Statuses.SUCCEEDED or Statuses.GAVEUP
// with Outcome.Err == nil and the last attempt's result on Outcome.Result.
func Drive(ctx context.Context, req Request, opts ...options.Option[Config]) Outcome {
	cfg := Config{
		maxAttempts:        1,
		cancelDrainTimeout: defaultCancelDrainTimeout,
		contextThreader:    ThreadNoContext(),
	}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if req.Attempt == nil {
		return Outcome{err: errors.New("pursue: nil AttemptFunc")}
	}
	if req.Goal == nil {
		req.Goal = AcceptCompleted()
	}

	spec := req.Spec
	for attempt := 1; ; attempt++ {
		res, watched, err := runAttempt(ctx, req, spec, cfg.cancelDrainTimeout, cfg.attemptTimeout)
		current := Attempt{Number: attempt, Spec: spec, Result: res}
		// err is harness-internal only (the drain timeout); the attempt's
		// own outcome lives entirely in res.
		if err != nil {
			report := AttemptReport{Attempt: current, Decision: Retry("")}
			if cfg.onAttempt != nil {
				cfg.onAttempt(report)
			}
			return Outcome{Attempts: attempt, Result: res, err: err}
		}
		// TerminalCancelled counts here too: an unwatched cancellation is
		// the parent ctx unwinding, and re-driving against a dead ctx would
		// burn the whole attempt budget in microseconds and mask the
		// cancellation as GAVEUP. A watched cancellation (the Watcher fired,
		// Drive cancelled the attempt itself) is exempt — the Goal still
		// arbitrates below.
		terminalErr := current.Result.Reason == runner.TerminalError ||
			current.Result.Reason == runner.TerminalCancelled
		if terminalErr && current.Result.Err != nil && !watched {
			report := AttemptReport{Attempt: current, Decision: Retry("")}
			if cfg.onAttempt != nil {
				cfg.onAttempt(report)
			}
			return Outcome{Attempts: attempt, Result: res, err: current.Result.Err}
		}
		decision := req.Goal.Evaluate(ctx, current)
		report := AttemptReport{Attempt: current, Decision: decision}
		if cfg.onAttempt != nil {
			cfg.onAttempt(report)
		}
		if decision.Done {
			return Outcome{Attempts: attempt, Result: res, Verified: isVerifiedGoal(req.Goal)}
		}
		// A watched attempt with a terminal error falls through to here (the
		// !watched early-return above is skipped) so the Goal still gets to
		// evaluate; surface the error now if it didn't declare done.
		if current.Result.Reason == runner.TerminalError && current.Result.Err != nil {
			return Outcome{Attempts: attempt, Result: res, err: current.Result.Err}
		}
		if attempt >= cfg.maxAttempts {
			return Outcome{Attempts: attempt, Result: res, gaveUp: true}
		}
		// Catch a parent cancellation that arrived without marking the
		// result (e.g. concurrently with a Watcher fire) before paying for
		// another attempt that can only return cancelled.
		if err := ctx.Err(); err != nil {
			return Outcome{Attempts: attempt, Result: res, err: fmt.Errorf("pursue: context cancelled between attempts: %w", err)}
		}
		spec = cfg.contextThreader(ctx, current, spec, decision)
	}
}

// ThreadFullTranscript re-drives with the prior attempt's entire message
// history as the next attempt's Context. Non-empty Decision.Feedback
// becomes the next Prompt; empty feedback keeps the prior Prompt.
//
// Convenient — the model sees everything it just did — but the transcript
// grows every retry: a failed multi-iteration attempt threads its whole
// history forward, and across several retries the context can balloon
// (stale tool results, repeated content, compaction pressure before the
// retry even starts). Opt in deliberately; ThreadNoContext is the default.
func ThreadFullTranscript() ContextThreader {
	return func(_ context.Context, attempt Attempt, next runner.TaskSpec, decision Decision) runner.TaskSpec {
		next.Context = attempt.Result.Messages
		if decision.Feedback != "" {
			next.Prompt = decision.Feedback
		}
		return next
	}
}

// ThreadNoContext re-drives from a clean slate: the next attempt carries
// no prior messages, only the Decision's feedback as its Prompt (or the
// original prompt when feedback is empty). This bounds context growth
// across attempts — the corrective feedback is expected to carry forward
// whatever the retry needs. It is Drive's default threader.
func ThreadNoContext() ContextThreader {
	return func(_ context.Context, _ Attempt, next runner.TaskSpec, decision Decision) runner.TaskSpec {
		next.Context = nil
		if decision.Feedback != "" {
			next.Prompt = decision.Feedback
		}
		return next
	}
}

func isVerifiedGoal(goal Goal) bool {
	_, ok := goal.(verifiedGoal)
	return ok
}

// runAttempt runs one AttemptFunc call. With no Watcher and no per-attempt
// timeout it runs synchronously — panic semantics are identical to a
// direct AttemptFunc call. Otherwise the attempt runs in a goroutine and
// runAttempt selects between three events: the attempt completing, the
// Watcher's channel closing (goal may be met mid-attempt), and the
// per-attempt deadline firing. Both the Watcher and the deadline cancel
// the attempt context; the deadline additionally overrides the result
// with a terminal ErrAttemptTimeout so a wedged AttemptFunc can't hang
// the loop.
//
// The returned error is NOT the attempt's outcome (that's wholly in the
// TaskResult) — it is the harness-internal ErrAttemptCancelDrainTimeout,
// set only when a Watcher fired and the cancelled attempt didn't drain in
// time. The AttemptFunc has no error channel, so the outcome rides the
// done channel as a bare TaskResult.
func runAttempt(
	ctx context.Context,
	req Request,
	spec runner.TaskSpec,
	cancelDrainTimeout time.Duration,
	attemptTimeout time.Duration,
) (runner.TaskResult, bool, error) {
	// Fast path: nothing to supervise → call inline and keep the original
	// (panic-transparent, goroutine-free) shape.
	if req.Watcher == nil && attemptTimeout <= 0 {
		return req.Attempt(ctx, spec), false, nil
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// nil channels block forever in select, so an absent watcher / timeout
	// simply never fires that case — no branching in the select itself.
	var watchCh <-chan struct{}
	if req.Watcher != nil {
		watchCh = req.Watcher(attemptCtx)
	}
	var timeoutCh <-chan time.Time
	if attemptTimeout > 0 {
		t := time.NewTimer(attemptTimeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- req.Attempt(attemptCtx, spec)
	}()

	select {
	case r := <-done:
		// Attempt finished naturally. The Watcher may have flipped
		// concurrently — non-blocking check, treat as goal met if so.
		if watchCh != nil {
			select {
			case <-watchCh:
				return r, true, nil
			default:
			}
		}
		return r, false, nil
	case <-watchCh:
		cancel()
		r, err := drainAttempt(ctx, done, cancelDrainTimeout)
		return r, true, err
	case <-timeoutCh:
		// Per-attempt deadline: cancel and give a cooperative AttemptFunc a
		// bounded window to settle, but the verdict is "timed out" either
		// way. watched stays false so Drive surfaces it as an ERRORED
		// Outcome carrying ErrAttemptTimeout.
		cancel()
		_, _ = drainAttempt(ctx, done, cancelDrainTimeout)
		return runner.TaskResult{
			Reason: runner.TerminalError,
			Err:    fmt.Errorf("%w after %s", ErrAttemptTimeout, attemptTimeout),
		}, false, nil
	}
}

func drainAttempt(ctx context.Context, done <-chan runner.TaskResult, timeout time.Duration) (runner.TaskResult, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-done:
		return r, nil
	case <-ctx.Done():
		return runner.TaskResult{}, fmt.Errorf("%w: %w", ErrAttemptCancelDrainTimeout, ctx.Err())
	case <-t.C:
		return runner.TaskResult{}, fmt.Errorf("%w after %s", ErrAttemptCancelDrainTimeout, timeout)
	}
}
