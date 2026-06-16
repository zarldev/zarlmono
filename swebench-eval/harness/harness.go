// Package harness defines the contract every coding-harness driver
// satisfies for SWE-bench-style evaluation. The shape is one method
// — Run(ctx, Task) Result — so adding a new harness to the comparison
// suite is a single struct + a single function away.
//
// Drivers are responsible for: invoking their backing harness, letting
// it produce a unified diff against the task's base commit, capturing
// summary metadata (duration, tool-call count, token usage when
// extractable). They are NOT responsible for evaluating whether the
// diff passes SWE-bench's tests — that's the official evaluator's
// job, and the runner package shells out to it after collecting all
// drivers' results.
package harness

import (
	"context"
	"time"
)

// Task is the input every Driver receives. Populated by the task
// loader from a SWE-bench-format dataset row.
type Task struct {
	// ID is the SWE-bench instance identifier (e.g. "django__django-11119").
	// Used as the run's TaskID downstream and as the join key when
	// reconciling diffs against the official evaluator's output.
	ID string

	// RepoPath is the absolute path to a worktree of the target repo
	// checked out at the task's base commit. The harness operates
	// on this directory; any diff is taken relative to it.
	RepoPath string

	// BaseCommit is the SHA of HEAD inside RepoPath when the harness
	// began. Drivers should diff against this rather than HEAD: if the
	// agent commits mid-run, HEAD moves and a HEAD-relative diff goes
	// empty, silently dropping a correct patch.
	BaseCommit string

	// Problem is the issue text the harness exposes to its agent.
	// SWE-bench calls this "problem_statement"; we use the plainer name.
	Problem string

	// Hints is the optional "hints_text" column from SWE-bench tasks —
	// short reviewer guidance. Empty for most tasks. Drivers may
	// prepend it to the problem statement or pass it through a
	// hint-aware channel, their choice.
	Hints string

	// Language is the primary language for this task, drawn from
	// SWE-bench Multilingual's per-instance language tag. Drivers may
	// use this to pick a language-appropriate sub-agent / prompt /
	// verifier; ignoring it is fine for harnesses with one universal
	// agent shape.
	Language string

	// FailToPass is the list of test names (in the upstream format —
	// "package.TestName" / "TestName" depending on language) that the
	// scoring harness expects to flip from failing to passing after
	// the agent's patch is applied. Drivers MAY surface these to the
	// agent so it knows exactly which tests need to pass; ignoring
	// them and letting the agent discover them by reading tests is
	// also valid. SWE-bench guarantees this list is non-empty; if it
	// is empty, the row was a loader bug.
	FailToPass []string
	// PassToPass is the list of tests already passing on HEAD that
	// must still pass after the agent's patch. Drivers usually don't
	// need to surface these — they're the regression guard, not the
	// target — but they're here for completeness.
	PassToPass []string

	// Timeout is the wall-clock cap for this task. The runner derives
	// it from a per-eval-run config; drivers should respect ctx
	// cancellation rather than enforcing the timeout themselves.
	Timeout time.Duration
}

// Result is the output every Driver produces. Diff is the load-bearing
// field — it's what SWE-bench's evaluator takes as input. Everything
// else is telemetry for the comparison report.
type Result struct {
	// Diff is the unified `git diff base..HEAD` output (including
	// untracked-file synthesis) captured at the end of the run.
	// Empty diff with Err=nil = the harness ran but didn't change
	// anything; SWE-bench will score that as "not resolved".
	Diff string

	// Duration is the wall-clock time the Run call took.
	Duration time.Duration

	// ToolCalls is the count of LLM-emitted tool calls during the run.
	// Drivers extract this however their backend exposes it
	// (zarlcode reads it from headless_runs; others may parse logs).
	ToolCalls int

	// TokensIn / TokensOut are prompt and completion token totals.
	// Zero is a valid "unknown" sentinel when a harness doesn't
	// report usage.
	TokensIn  int64
	TokensOut int64

	// Iterations is the agent-loop iteration count (LLM call + tool
	// dispatch cycles). Same "0 = unknown" convention.
	Iterations int

	// TerminalReason is the driver-owned, stable wire string for why the
	// run ended — persisted in the results DB and grouped on by the report,
	// so each driver maps its own outcome into a fixed vocabulary it
	// controls (the zarlcode driver uses "completed", "max_iterations",
	// "error", "cancelled"). It is NOT the runner's internal enum value:
	// a driver maps explicitly so an upstream rename can't silently
	// regroup historical results. Drivers without a comparable concept
	// leave it empty.
	TerminalReason string

	// Escalated reports whether this run reached for a stronger model
	// or cloud provider mid-task. Always false for harnesses without
	// an escalation path.
	Escalated bool

	// Verified reports that the run's success was confirmed by a
	// world-checking goal — for the zarlcode driver, the official
	// SWE-bench verifier actually resolved the task — not merely that
	// the agent claimed completion. False when the driver ran a single
	// unverified attempt (VerifiedAttempts <= 1) or solved nothing.
	// Lets the report separate "agent stopped" from "task provably
	// resolved."
	Verified bool

	// Provider + Model identify the LLM the driver invoked. Both
	// are informational — the eval framework groups results by
	// (driver, provider, model) so "same harness, different model"
	// is a first-class comparison axis. Empty when the driver
	// doesn't surface this (e.g. a future toolkit that hides its
	// backend behind a single endpoint).
	Provider string
	Model    string

	// GuardrailRejections counts guardrail-enforced tool-call rejections
	// per guardrail name across the run — the trigger telemetry the
	// ablation report reads ("fanout fired N times on this arm"). Nil
	// when the driver doesn't surface a transcript to count from.
	GuardrailRejections map[string]int

	// Attempts is how many agent attempts the run consumed (1 for a
	// single-shot run; up to VerifiedAttempts under a verified re-drive).
	Attempts int

	// AttemptVerdicts is the per-attempt verifier history for verified
	// runs — what the goal oracle said after each attempt. Without it a
	// final unresolved patch is uninterpretable: "attempt 1 passed
	// in-run but the final score failed" (grader flake) reads identically
	// to "every attempt failed" (model limit). Nil for single-shot runs.
	AttemptVerdicts []AttemptVerdict

	// HarnessLog is the raw stdout/stderr captured from the driver's
	// subprocess. Useful for debugging individual failures.
	HarnessLog string

	// Err is non-nil for terminal driver failures (subprocess crash,
	// timeout, missing binary). A driver's underlying harness
	// reporting "I couldn't solve this task" returns Err=nil — that's
	// not a driver-level failure, it's a successful run with an
	// unsuccessful agent outcome.
	Err error
}

// AttemptVerdict is one verified-attempt outcome: what the in-run goal
// oracle (the official SWE-bench evaluator) said about that attempt's
// patch. Serialized as JSON into eval_results.attempt_verdicts.
type AttemptVerdict struct {
	Attempt  int  `json:"attempt"`
	Resolved bool `json:"resolved"`
	// EmptyPatch marks an attempt rejected before invoking the evaluator
	// (no diff to verify).
	EmptyPatch bool `json:"empty_patch,omitempty"`
	// Error carries the verifier-could-not-run message; empty when the
	// evaluator ran and returned a clean verdict.
	Error string `json:"error,omitempty"`
}

// Driver is a harness adapter — one implementation per coding harness
// under comparison (zarlcode today; any external harness via its own
// adapter). The runner package treats every Driver identically — feeds
// Tasks in, collects Results out — so adding a new harness to the
// comparison suite is a single Driver implementation away.
type Driver interface {
	// Name identifies the harness in reports. Stable identifier;
	// don't include version or model details here.
	Name() string

	// Run executes one task. Returning a non-nil Result.Err signals a
	// driver-level failure (subprocess crashed, binary missing).
	// Returning an empty Result.Diff with no Err signals "the harness
	// ran cleanly but didn't change anything" — SWE-bench scores that
	// as a non-resolution.
	Run(ctx context.Context, t Task) Result
}
