package runner

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Steerer drains queued user messages between iterations of Runner.Run.
// The runner calls Drain at the top of every iteration (after the
// ConversationLock yield, before message shaping). Drain MUST NOT
// block: it yields whatever messages are ready right now and returns.
// Yielding nothing is the normal idle case.
//
// Returning iter.Seq lets the runner stop draining mid-stream if it
// ever wants to cap injected count; today it always consumes the
// whole iterator.
//
// Implementations are typically owned by an interactive harness (TUI,
// REPL) that lets the user type while a turn is running and accumulates
// those lines into a queue. Headless runners (background tasks,
// scheduler-launched runs) leave the steerer unset.
//
// # Concurrency
//
// Drain runs on the runner's goroutine. The harness producing
// messages (Append/enqueue from a UI event loop or notification
// callback) typically runs elsewhere. Implementations are responsible
// for synchronising the two sides — a sync.Mutex around the slice is
// the canonical shape; see zarlcode/tui's queueState.
//
// One Steerer per Runner is the supported model. If the same Steerer
// is shared across concurrent Runs, both will Drain from the same
// queue and split its contents arbitrarily.
type Steerer interface {
	Drain(ctx context.Context) iter.Seq[llm.Message]
}

// WithSteerer installs a Steerer the runner consults at every iteration
// boundary. Without this option the runner runs unsteered (current
// behaviour).
func WithSteerer(s Steerer) options.Option[Runner] {
	return func(r *Runner) { r.steerer = s }
}
