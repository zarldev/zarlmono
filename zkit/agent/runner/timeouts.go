package runner

import "time"

// timeouts groups the runner's three wall-clock guards. They are NOT
// nested deadlines on one clock — each scopes a different phase, and
// holding them in one value is what documents which phase each bounds:
//
//   - iteration bounds the LLM call plus the stream drain of a single
//     iteration (Complete through drainStream). It does NOT cover tool
//     dispatch: drainStream cancels the per-iteration context before
//     dispatch runs, so a long tool batch is bounded by tool (below) and
//     the outer ctx, not by iteration. Its job is to recover a model
//     wedged in runaway thinking with a diagnostic ErrIterationTimeout
//     rather than a SIGKILL from an outer deadline; tune it to the
//     slowest legitimate stream you expect.
//   - streamIdle is a tighter gate *within* the stream: it fires only on
//     dead silence (no chunk for the budget), catching a hung connection
//     without aborting a slow-but-progressing response. Cheaper than
//     iteration because an active stream resets it on every chunk.
//   - tool bounds a single tool's execution, applied as a context
//     deadline around each dispatch. A tool that ignores ctx keeps
//     running in its own goroutine past the deadline, but the runner
//     stops waiting and records a Timeout result so the next iteration
//     isn't blocked.
//
// Zero disables a guard. iteration and streamIdle default to disabled;
// tool defaults to defaultToolTimeout.
type timeouts struct {
	iteration  time.Duration
	streamIdle time.Duration
	tool       time.Duration
}
