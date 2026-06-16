package compact

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// TieredDefaultTargetBytes is the byte budget the Tiered engine sizes
// its phase thresholds against. ~32k tokens at 4 chars/token — sized
// for the 32k-context model family. For larger windows pass an
// explicit TargetBytes on the struct.
const TieredDefaultTargetBytes = 128 * 1024

// tieredToolTruncateChars caps a tool result body in Phase 1. Sized so
// tool results that carry small file diffs or line ranges stay
// re-referenceable inside the same compaction window.
const tieredToolTruncateChars = 256

// tieredAssistantTruncateChars caps assistant narrative content in
// Phase 2. Trims more aggressively than [Structural]'s 1KB ceiling
// because Phase 2 only fires when the conversation is already at
// 75% of the configured budget.
const tieredAssistantTruncateChars = 256

// Tiered is a progressive compactor that escalates aggressiveness in
// three phases keyed to the current history byte size. Messages carry
// no typed message-kind tags, so tool/assistant role + content size
// drive the phase decision.
//
// Compared to [Structural], which trims uniformly every iteration
// whether or not pressure is real, Tiered does nothing below 60% of
// the configured budget and ramps trimming as the history grows.
// Reasoning (assistant content) is preserved longest — that's the
// model's interpretive context for the next turn.
//
// Phase 1 (>= 60% budget): truncate tool result bodies to the first
// ~256 chars. ToolCallID preserved so the call -> result link stays
// valid; assistant content untouched.
//
// Phase 2 (>= 75% budget): Phase 1 + assistant narrative content
// trimmed to the first ~256 chars. ToolCalls on assistant messages
// preserved verbatim — only the prose alongside them is cut.
//
// Phase 3 (>= 90% budget): Phase 2 + tool result bodies replaced
// with a single-line placeholder regardless of size; assistant
// narrative content cleared entirely. ToolCalls + ToolCallIDs still
// preserved so the action sequence is recoverable. The agent can
// re-run any tool to recover content if it needs to.
//
// Below 60% budget Tiered is a pure no-op — history flows through
// untouched, no allocation.
type Tiered struct {
	// TargetBytes is the byte budget against which phase thresholds
	// are sized. Zero falls back to [TieredDefaultTargetBytes]. Pass
	// (ctxWindow * 4) for a model-specific budget where ctxWindow is
	// in tokens.
	TargetBytes int

	// Phase1Threshold / Phase2Threshold / Phase3Threshold are
	// fractions of TargetBytes that gate each phase. Zero falls back
	// to 0.60 / 0.75 / 0.90 respectively. Thresholds must be strictly
	// increasing — Phase 2 only fires if Phase 1 didn't bring bytes
	// under Phase 2's trigger, and so on.
	Phase1Threshold float64
	Phase2Threshold float64
	Phase3Threshold float64
}

// NewTiered returns a Tiered compactor sized against the model's
// context window expressed in tokens. The byte budget is half the
// window in chars (4 chars/token × tokens / 2), so Phase 1 (60% of
// budget) fires at roughly 30% of the model's actual capacity —
// comfortable headroom for the next prompt + response.
//
// Pass 0 to fall back to [TieredDefaultTargetBytes], which is sized
// for 32k-token windows. Computed budgets smaller than the default
// are clamped up to it: a misconfigured / too-small window would
// otherwise yield a trigger tight enough to trip Phase 1 on every
// iteration, which was the original "still aggressively compacting
// with tons of headroom" bug — the constructor was hardcoded to the
// 32k default while the runtime model was 1M.
func NewTiered(ctxWindowTokens int) *Tiered {
	target := TieredDefaultTargetBytes
	if ctxWindowTokens > 0 {
		budget := ctxWindowTokens * 4 / 2
		if budget > target {
			target = budget
		}
	}
	return &Tiered{
		TargetBytes:     target,
		Phase1Threshold: 0.60,
		Phase2Threshold: 0.75,
		Phase3Threshold: 0.90,
	}
}

// WouldReduceBytes implements [Prober]. Tiered is a no-op below its
// Phase 1 trigger — the runner's compaction gate uses this to skip
// the engine entirely when the history is under pressure. Returns
// an estimate (total bytes above the phase-1 trigger) rather than a
// precise count; the runner only checks for `> 0` so the magnitude
// only matters for telemetry.
func (t *Tiered) WouldReduceBytes(history []llm.Message, keepRecent int) int {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent {
		return 0
	}
	target := t.TargetBytes
	if target <= 0 {
		target = TieredDefaultTargetBytes
	}
	p1 := t.Phase1Threshold
	if p1 <= 0 {
		p1 = 0.60
	}
	totalBytes := historyBytes(history)
	trigger := int(float64(target) * p1)
	if totalBytes < trigger {
		return 0
	}
	// Conservative estimate — the difference between current size
	// and the Phase 1 trigger is the minimum the engine would trim.
	return totalBytes - trigger
}

// Compact implements [Compactor].
func (t *Tiered) Compact(_ context.Context, history []llm.Message, keepRecent int) (Result, error) {
	if keepRecent < 0 {
		keepRecent = 0
	}
	target := t.TargetBytes
	if target <= 0 {
		target = TieredDefaultTargetBytes
	}
	p1 := t.Phase1Threshold
	p2 := t.Phase2Threshold
	p3 := t.Phase3Threshold
	if p1 <= 0 {
		p1 = 0.60
	}
	if p2 <= 0 {
		p2 = 0.75
	}
	if p3 <= 0 {
		p3 = 0.90
	}

	totalBytes := historyBytes(history)
	t1 := int(float64(target) * p1)
	t2 := int(float64(target) * p2)
	t3 := int(float64(target) * p3)

	// Fast path: below Phase 1 trigger or nothing eligible to trim.
	if totalBytes < t1 || len(history) <= keepRecent {
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineTiered,
		}, nil
	}

	// Always protect the leading system message (if present) and the
	// most-recent keepRecent messages.
	head := 0
	if len(history) > 0 && history[0].Role == llm.RoleSystem {
		head = 1
	}
	end := len(history) - keepRecent
	if end <= head {
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineTiered,
		}, nil
	}

	// Phase 1.
	out := tieredPhase1(history, head, end)
	if historyBytes(out) < t2 {
		return tieredResult(out, totalBytes-historyBytes(out), 1), nil
	}

	// Phase 2 = Phase 1 + assistant content trim.
	out = tieredPhase2(out, head, end)
	if historyBytes(out) < t3 {
		return tieredResult(out, totalBytes-historyBytes(out), 2), nil
	}

	// Phase 3 = Phase 2 + tool placeholder + assistant content clear.
	out = tieredPhase3(out, head, end)
	return tieredResult(out, totalBytes-historyBytes(out), 3), nil
}

func tieredResult(out []llm.Message, trimmed, phase int) Result {
	return Result{
		History:      out,
		Engine:       EngineTiered,
		BytesTrimmed: trimmed,
		Warning: fmt.Sprintf(
			"compacted via tiered phase %d: trimmed ~%d bytes",
			phase, trimmed),
	}
}

// historyBytes is a cheap proxy for token usage — the cumulative
// content size of every message including its tool call arguments
// and multimodal parts. Uses the same [messageChars] helper as
// [AdaptiveKeepRecent] so adaptive sizing and phase thresholds
// agree on what "bigger" means.
func historyBytes(messages []llm.Message) int {
	var n int
	for _, m := range messages {
		n += messageChars(m)
	}
	return n
}

// tieredPhase1 truncates tool result bodies in the older range.
// Returns a new slice; input is not mutated.
func tieredPhase1(history []llm.Message, head, end int) []llm.Message {
	out := make([]llm.Message, len(history))
	copy(out, history)
	for i := head; i < end; i++ {
		msg := out[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		if len(msg.Content) <= tieredToolTruncateChars {
			continue
		}
		kept := clipToRune(msg.Content, tieredToolTruncateChars)
		removed := len(msg.Content) - len(kept)
		msg.Content = fmt.Sprintf(
			"%s\n[truncated — %d chars elided post-compact]",
			kept, removed)
		out[i] = msg
	}
	return out
}

// tieredPhase2 trims assistant narrative content (the prose
// alongside tool calls). ToolCalls on the message are preserved so
// the action trail stays intact. Operates on a slice already
// produced by tieredPhase1 — does not re-truncate tool results.
func tieredPhase2(history []llm.Message, head, end int) []llm.Message {
	out := make([]llm.Message, len(history))
	copy(out, history)
	for i := head; i < end; i++ {
		msg := out[i]
		if msg.Role != llm.RoleAssistant {
			continue
		}
		if len(msg.Content) <= tieredAssistantTruncateChars {
			continue
		}
		kept := clipToRune(msg.Content, tieredAssistantTruncateChars)
		removed := len(msg.Content) - len(kept)
		msg.Content = fmt.Sprintf(
			"%s\n[reasoning trimmed — %d chars elided]",
			kept, removed)
		out[i] = msg
	}
	return out
}

// tieredPhase3 collapses tool result bodies to a single-line
// placeholder and clears assistant narrative content entirely.
// ToolCalls + ToolCallIDs are preserved so the call sequence
// remains valid against provider APIs that require paired
// tool_call / tool_result messages.
func tieredPhase3(history []llm.Message, head, end int) []llm.Message {
	out := make([]llm.Message, len(history))
	copy(out, history)
	for i := head; i < end; i++ {
		msg := out[i]
		switch msg.Role {
		case llm.RoleTool:
			placeholder := fmt.Sprintf(
				"[tool result elided post-compact — original ~%d bytes. Re-run to recover.]",
				len(msg.Content))
			msg.Content = placeholder
		case llm.RoleAssistant:
			msg.Content = ""
		default:
			continue
		}
		out[i] = msg
	}
	return out
}
