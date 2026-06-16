package runner

import (
	"github.com/zarldev/zarlmono/zkit/options"
)

// CompletionGate guards the "no tool calls → completed" exit. When the
// model emits an iteration with no tool calls, the runner normally
// treats that as a clean terminal state. For a task that REQUIRES a
// durable change — a SWE-bench fix, a refactor — that exit is wrong if
// the run never actually mutated anything: the result is a confident
// final message with an empty patch, an attempt silently spent on
// nothing.
//
// The gate is consulted at exactly that exit. It receives workDone —
// whether the run has made at least one successful mutating tool call
// (edit / write / write_append / apply_patch; see ToolSpec.Mutates) —
// and the finalised assistant content. A non-empty Correction means
// "do NOT complete": the runner injects the correction as a user
// message and continues the loop, so the model can make the change
// within the SAME Run rather than burning the attempt and relying on a
// downstream re-drive. Bounded by the decision's MaxCorrections so a
// genuinely stuck model still terminates instead of looping to the cap.
//
// Consulted only on the no-tool-call terminal turn, AFTER any
// TurnQuality hook has had its chance — TurnQuality catches empty
// CONTENT, this catches empty WORK. The two are orthogonal: a model
// can write a fluent "the fix is straightforward" essay (passing
// TurnQuality) while having edited nothing (caught here).
//
// Limitation: workDone keys on tool capability (ToolSpec.Mutates), so
// changes made only through `bash` (e.g. `sed -i`) are NOT counted —
// bash leaves Mutates unset. In eval mode shell_policy already blocks
// output redirection and the prompt steers to the dedicated edit/write
// tools, so this is rare; a consumer that needs authoritative coverage
// should gate on the actual worktree diff instead.
//
// Implementations must be safe for concurrent use — a Runner is
// reusable across concurrent Runs.
type CompletionGate interface {
	Inspect(workDone bool, content string) CompletionDecision
}

// CompletionDecision is the runner-side action requested by a
// CompletionGate. A non-empty Correction blocks the completion and is
// injected as a user message before the loop continues. MaxCorrections
// caps how many times this gate may hold a single Run; zero means
// unlimited (bounded only by the runner's MaxIterations).
type CompletionDecision struct {
	Correction     string
	MaxCorrections int
}

// RequireWork is the default CompletionGate: it refuses to let a Run
// complete on a no-tool-call turn until the run has made at least one
// successful mutating tool call. The zero value is usable; the empty
// struct exists so an option caller can install it by type.
type RequireWork struct {
	// Message overrides the correction surfaced to the model. Zero
	// value uses defaultRequireWorkMessage. Override for task-specific
	// phrasing (SWE-bench wants "produce the diff", a refactor wants
	// "apply the change").
	Message string

	// MaxCorrections caps gate holds per Run. Zero leaves the retry
	// bounded only by the runner's MaxIterations — almost never what
	// you want, since a model determined to do nothing would loop to
	// the cap. Set a small value (2 is typical).
	MaxCorrections int
}

const defaultRequireWorkMessage = "You're about to end the task, but you haven't made any change yet — " +
	"no file has been edited or written, so the resulting patch would be empty. This task requires a " +
	"concrete code change. Locate the relevant source file, make the edit with the edit/write tools, and " +
	"verify it before concluding. Do not finish with an empty patch: if you believe no change is needed, " +
	"that is almost certainly wrong here — re-read the task and the failing behaviour, then make the fix."

// Inspect implements CompletionGate. Returns the configured correction
// when the run has done no mutating work; otherwise a zero decision so
// the runner completes normally.
func (g RequireWork) Inspect(workDone bool, _ string) CompletionDecision {
	if workDone {
		return CompletionDecision{}
	}
	msg := defaultRequireWorkMessage
	if g.Message != "" {
		msg = g.Message
	}
	return CompletionDecision{Correction: msg, MaxCorrections: g.MaxCorrections}
}

// WithCompletionGate installs the gate the runner consults at the
// no-tool-call terminal exit. A nil gate (the default) preserves the
// original "no tool calls == complete" behaviour, keeping the change
// opt-in for consumers that don't want it.
func WithCompletionGate(g CompletionGate) options.Option[Runner] {
	return func(r *Runner) { r.completionGate = g }
}
