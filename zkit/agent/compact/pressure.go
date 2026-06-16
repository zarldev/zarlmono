package compact

import (
	"context"
	"sync/atomic"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// PressureGated wraps another [Compactor] and refuses to advertise
// savable bytes (via [Prober.WouldReduceBytes]) unless the latest
// observed [llm.Usage] crosses the configured context-pressure
// threshold.
//
// The wrapper exists because the runner's per-iteration compaction
// call uses the prober as a "should I run?" gate: every Compactor
// with a positive WouldReduceBytes gets called on every iteration.
// [Summary] and [Executive] report "the full older slice is
// savable" optimistically — true bytes-wise but pointless to act on
// when the context window is barely scratched. On a 1M-context
// model with 11k of usage, the bare Summary prober makes the runner
// compact every turn, even though there's 985k tokens of headroom.
//
// PressureGated answers the runner's gate with zero (no work) when
// usage is below threshold, so the runner skips Compact entirely.
// The consumer's proactive-compaction trigger (which has its own
// usage check) and the reactive overflow path are unaffected — they
// call inner.Compact directly.
//
// # Wiring the usage signal
//
// Usage observations arrive via [PressureGated.ObserveUsage]. The
// expected wiring is for the consumer to subscribe to the runner's
// IterationCompleted event and forward each iteration's Usage. Before
// any observation lands, the gate stays closed (no signal to act on).
// The atomic.Pointer makes the field safe for concurrent observers
// and concurrent probe calls — required because a single runner
// servicing concurrent Runs publishes events from multiple goroutines
// (see EventSink's concurrency contract).
//
// Configuration:
//
//   - Inner: the underlying engine (Summary, Structural, …).
//   - Window: the model's context window in tokens. Zero disables
//     the gate (defers to inner) — safest fallback when window
//     isn't known.
//   - Reserve: tokens held back from the window. Threshold is
//     Window - Reserve.
type PressureGated struct {
	Inner   Compactor
	Window  int
	Reserve int

	// usage holds the most recent observation pushed by ObserveUsage.
	// Atomic so the runner's publish goroutine and probe goroutine
	// (same goroutine in current Run, different goroutines under
	// concurrent Runs) don't race. nil load means "no observation
	// yet" → gate closed.
	usage atomic.Pointer[llm.Usage]
}

// UsageObserver is the optional interface the runner detects on its
// configured Compactor to push per-iteration [llm.Usage] before the
// next compaction gate check. Implementations should treat each call
// as a snapshot — concurrent callers under multiplexed Runs are
// possible and the latest observation wins.
type UsageObserver interface {
	ObserveUsage(*llm.Usage)
}

// ObserveUsage records the latest llm.Usage for the gate to consult.
// Safe for concurrent callers. nil is a valid observation — treated
// as "this iteration didn't carry usage" and leaves the previously
// observed value in place rather than overwriting with absence.
func (p *PressureGated) ObserveUsage(u *llm.Usage) {
	if u == nil {
		return
	}
	p.usage.Store(u)
}

// Compact passes through to the inner engine unchanged. The gate
// exists at the prober level — once the runner has decided to call
// Compact, the wrapper trusts that decision and lets the inner do
// the real work.
func (p *PressureGated) Compact(ctx context.Context, history []llm.Message, keep int) (Result, error) {
	return p.Inner.Compact(ctx, history, keep)
}

// WouldReduceBytes implements [Prober]. Returns 0 (closed gate)
// when usage is below the pressure threshold; defers to the inner
// engine's prober when pressure is real or when the gate can't
// evaluate (no window configured, no observation yet).
func (p *PressureGated) WouldReduceBytes(history []llm.Message, keep int) int {
	if p.Window <= 0 {
		return innerProbeOrUnknown(p.Inner, history, keep)
	}
	usage := p.usage.Load()
	if usage == nil {
		// No turn has completed — no signal to act on. The consumer
		// will compact proactively before the first turn if it
		// needs to; the runner has nothing to gate on here.
		return 0
	}
	threshold := p.Window - p.Reserve
	if threshold <= 0 {
		// Reserve ≥ window means compaction is effectively disabled.
		return 0
	}
	if usage.TotalTokens <= threshold {
		return 0
	}
	return innerProbeOrUnknown(p.Inner, history, keep)
}

// innerProbeOrUnknown forwards to the inner compactor's prober when
// present; for engines without a [Prober] implementation it returns
// a positive sentinel so the runner's gate falls open (preserves
// the existing "no prober means call every iteration" contract).
func innerProbeOrUnknown(c Compactor, history []llm.Message, keep int) int {
	if p, ok := c.(Prober); ok {
		return p.WouldReduceBytes(history, keep)
	}
	return 1
}
