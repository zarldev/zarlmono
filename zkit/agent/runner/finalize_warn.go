package runner

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/options"
)

// FinalizeWarn configures the cap-warning nudge: when the iteration
// loop has FinalizeWarn.RemainingThreshold iterations left (including
// the one about to start), the runner injects a synthetic user
// message asking the model to wrap up and commit to a final answer
// or implementation before the cap fires. Fires exactly once per
// Run; subsequent iterations within the threshold window don't
// re-inject.
//
// Without this, a small model deep in tool calls gets cut off
// mid-thought at MaxIterations with no final reply, the
// TerminalMaxIterations result lands with an empty FinalContent,
// and downstream consumers (TUI transcript, headless run records,
// benchmark scorers) have no useful output to attribute to the
// model. The warning gives the model a clear "this is your last
// chance" signal so it can produce a final answer in the remaining
// turns.
//
// Zero value disables the hook — the runner runs exactly as it did
// pre-C2.
type FinalizeWarn struct {
	// RemainingThreshold is the iterations-remaining count at which
	// the warning fires. "5" means the warning lands at the start of
	// the iteration where 5 iterations remain (including the current
	// one). Values <= 0 disable the hook.
	RemainingThreshold int

	// Message overrides the default warning text. Use this for
	// benchmark-specific phrasing — GAIA needs "Answer: <value>",
	// SWE-bench needs "produce the diff now", etc. Zero value uses
	// defaultFinalizeWarnMessage, which is a generic "wrap up"
	// nudge that names the actual remaining-iteration count.
	Message string
}

// finalizeWarnMessage renders the warning text for the given
// remaining-iteration count. Exposed as a function (not a method)
// so tests can verify the default phrasing without constructing a
// FinalizeWarn just to call .renderDefault.
func finalizeWarnMessage(remaining int, override string) string {
	if override != "" {
		return override
	}
	return fmt.Sprintf(
		"You have %d iteration%s left before the loop terminates. "+
			"Wrap up: produce your final answer in plain text, or commit "+
			"to the implementation you've been building. If you're tracking "+
			"a plan or checklist, make sure it reflects reality before "+
			"signing off. Do NOT start a new investigation or tool chain "+
			"— there isn't time. If you need a fact you don't already "+
			"have, make your best inference from what's already in context.",
		remaining, pluralS(remaining))
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// WithFinalizeWarn installs a cap-warning nudge configuration. A
// FinalizeWarn with RemainingThreshold <= 0 (the default zero value)
// silently disables the feature — useful for headless runs where
// the cap exists purely as a watchdog and a wrap-up nudge would
// add noise rather than signal.
func WithFinalizeWarn(fw FinalizeWarn) options.Option[Runner] {
	return func(r *Runner) { r.finalizeWarn = fw }
}
