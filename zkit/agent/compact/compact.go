// Package compact holds the engines that shrink an in-progress
// conversation when the context window is under pressure. Four
// implementations ship today:
//
//   - [Structural] is the historical default — it never calls a
//     model. Older messages are walked and bulky bodies (long
//     assistant reasoning, large tool result blobs) are replaced
//     with elision markers while the conversation's shape (user
//     intent, tool_call_ids, assistant turn structure) is preserved
//     verbatim. Always trims, regardless of pressure.
//
//   - [Tiered] is the progressive sibling of Structural — also
//     model-free, but does nothing below 60% of a configurable
//     byte budget and escalates trimming through three phases as
//     the conversation grows. Preserves reasoning longest (the
//     model's interpretive context for the next turn). Suited to
//     long sessions where most iterations are small but
//     occasionally the working set balloons.
//
//   - [Summary] feeds the older portion of the conversation to a
//     small / cheap LLM and replaces those messages with a single
//     EngineSummary assistant message. Opt-in because summarising a
//     tool result loses the actual bytes the agent might want to
//     re-reference; useful when the agent prefers a compressed
//     narrative over re-fetching detail.
//
//   - [Executive] is Summary plus structured state (plan progress,
//     working files, tool usage) drawn from a consumer-supplied
//     [StateProvider]. The richest briefing format; same LLM cost
//     as Summary with markedly higher signal density.
//
// All four engines implement [Compactor]. The shell picks one at
// construction time from settings and re-uses it for every
// proactive / manual compaction.
//
// The runner consults this same Compactor interface at the top of
// every iteration > 0 for turn-by-turn auto-compaction (see
// zkit/agent/runner.WithCompactor). The shell's /compact slash command
// uses the same interface for manual compaction. One shape, both
// surfaces — keepRecent is the contract; engines decide what (if
// anything) to trim beyond preserving that recent window.
package compact

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Engine labels for the built-in compactors, as reported on
// CompactionApplied events. Structural and Tiered are model-free; Summary
// and Executive need an LLM client.
const (
	EngineStructural = "structural"
	EngineTiered     = "tiered"
	EngineSummary    = "summary"
	EngineExecutive  = "executive"
	EngineHandover   = "handover"
)

// Result is what an engine returns from a Compact call. History is
// the new conversation slice; Warning is a one-line note for the
// event log (typically "trimmed ~N bytes from M msgs"); Engine
// identifies which engine produced the result so the LLM-state
// pane can render "compacted via summary" next to the counts.
//
// BytesTrimmed is the engine's own count of bytes elided from older
// messages. The structural engine reports the sum of content removed
// from in-place truncated messages; the summary engine reports the
// size of the older slice it replaced with a single summary message.
// Callers (the runner) use this to decide whether to publish a
// CompactionApplied event even when no message was removed — without
// this flag a structural-only trim is invisible to subscribers, since
// the message count doesn't change.
type Result struct {
	History      []llm.Message
	Warning      string
	Engine       string
	BytesTrimmed int
}

// Compactor is the interface the shell consults for every compaction.
// keepRecent is the number of most-recent messages preserved verbatim
// (the immediate working set the agent is actively building on);
// negative values are clamped to zero.
//
// Returns the new history, a one-line warning suitable for the event
// log, an engine label for UI, and an optional error. Returning a
// shorter slice is normal; returning the input unchanged is fine when
// nothing was bulky enough to compact.
type Compactor interface {
	Compact(ctx context.Context, history []llm.Message, keepRecent int) (Result, error)
}

// Prober is the OPTIONAL extension a compactor implements when it can
// cheaply estimate, without doing the work, how many bytes a Compact
// call would save on the given history. Consumers use this to skip
// proactive compactions that would be no-ops — calling structural
// compaction on a history with no oversized older messages, for
// instance, just burns CPU and produces a "nothing large enough to
// elide" notice the user reads as broken behaviour.
//
// Implementations MUST be cheap (one pass over history at most) and
// pure: no I/O, no allocation past a couple of ints. The contract is
// "would Compact reduce bytes?" — zero means "no point firing";
// positive means "go ahead." Signed int leaves room for a future
// "estimated NET delta" extension; today implementations return 0
// or a positive estimate.
//
// Engines without a meaningful probe (Summary, Executive — they
// always produce a briefing when there's anything older to summarise)
// can still implement this for completeness; the consumer falls
// back to the token-pressure check alone when the compactor doesn't
// satisfy Prober.
type Prober interface {
	WouldReduceBytes(history []llm.Message, keepRecent int) int
}

// Func adapts a plain function to the [Compactor] interface for
// one-line wrappers — useful in tests.
type Func func(ctx context.Context, history []llm.Message, keepRecent int) (Result, error)

// Compact satisfies [Compactor].
func (f Func) Compact(ctx context.Context, history []llm.Message, keepRecent int) (Result, error) {
	return f(ctx, history, keepRecent)
}

// ParseEngine canonicalises a settings string to an engine name.
// Returns EngineStructural for the empty string. The Summary and
// Executive engines need additional configuration (an LLM client +
// model + state provider) so they aren't constructed here; the
// shell builds them directly with [NewSummary] / [NewExecutive].
// Tiered is constructed by the shell with workspace-derived
// TargetBytes via [NewTiered].
func ParseEngine(name string) (string, error) {
	switch name {
	case "", EngineStructural:
		return EngineStructural, nil
	case EngineTiered:
		return EngineTiered, nil
	case EngineSummary:
		return EngineSummary, nil
	case EngineExecutive:
		return EngineExecutive, nil
	case EngineHandover:
		return EngineHandover, nil
	}
	return "", fmt.Errorf("unknown compact engine %q (want structural | tiered | summary | executive | handover)", name)
}
