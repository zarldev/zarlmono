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

// tieredAssistantTruncateChars bounds the operational-state capsule retained
// for each older assistant message in Phases 2 and 3.
const tieredAssistantTruncateChars = 256

const (
	tieredToolTruncationStart      = "\n[truncated — "
	tieredToolTruncationEnd        = " chars elided post-compact]"
	tieredAssistantTruncationStart = "\n[reasoning trimmed — "
	tieredAssistantTruncationEnd   = " chars elided; operational tail retained]\n"
	tieredToolElisionStart         = "[tool result elided post-compact — original ~"
	tieredToolElisionEnd           = " bytes. Re-run to recover.]"
)

const tieredAssistantCapsuleEdgeChars = tieredAssistantTruncateChars / 2

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
// ~256 chars and profitably elide older tool attachments. ToolCallID is
// preserved so the call -> result link stays valid; assistant content is untouched.
//
// Phase 2 (>= 75% budget): Phase 1 + assistant narrative content
// trimmed to the first ~256 chars. ToolCalls on assistant messages
// preserved verbatim — only the prose alongside them is cut.
//
// Phase 3 (>= 90% budget): Phase 2 + tool result bodies replaced
// with a single-line placeholder when that is smaller. The bounded
// head-and-tail assistant capsules, ToolCalls, and ToolCallIDs remain,
// preserving projected operational state and provider-required tool pairing.
// As an explicit native replay boundary, complete ContinuationItems are dropped
// from older messages (individual payloads are never truncated).
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

// WouldReduceBytes implements [Prober]. It mirrors Tiered's pressure-selected
// phases and reports only profitable reductions in the eligible older range.
// The estimate is allocation-free and exact for the retained byte accounting.
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
	t1 := int(float64(target) * p1)
	t2 := int(float64(target) * p2)
	t3 := int(float64(target) * p3)

	head := 0
	if history[0].Role == llm.RoleSystem {
		head = 1
	}
	end := len(history) - keepRecent
	if end <= head {
		return 0
	}

	var totalBytes, phase1Savings, phase2Savings, phase3Savings int
	for i, msg := range history {
		totalBytes += messageChars(msg)
		if i < head || i >= end {
			continue
		}
		if msg.Role == llm.RoleTool {
			plan := planTieredToolPhase1(msg)
			phase1Savings += plan.saved
			placeholderBytes := len(tieredToolElisionStart) + decimalDigits(plan.contentBytes) + len(tieredToolElisionEnd)
			phase3Savings += max(plan.contentBytes-placeholderBytes, 0)
		}
		if msg.Role == llm.RoleAssistant {
			phase2Savings += tieredAssistantSavings(msg.Content)
		}
		for _, item := range msg.ContinuationItems {
			phase3Savings += item.ByteLen()
		}
	}
	if totalBytes < t1 {
		return 0
	}
	afterPhase1 := totalBytes - phase1Savings
	if afterPhase1 < t2 {
		return phase1Savings
	}
	afterPhase2 := afterPhase1 - phase2Savings
	if afterPhase2 < t3 {
		return phase1Savings + phase2Savings
	}
	return phase1Savings + phase2Savings + phase3Savings
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
			History: llm.CloneMessages(history),
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
			History: llm.CloneMessages(history),
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

	// Phase 3 = Phase 2 + smaller tool placeholders and an explicit native
	// continuation boundary for the older range.
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

// tieredPhase1 truncates tool result bodies and elides their attachments in
// the older range. It returns a new slice without mutating history.
func tieredPhase1(history []llm.Message, head, end int) []llm.Message {
	out := llm.CloneMessages(history)
	for i := head; i < end; i++ {
		msg := out[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		plan := planTieredToolPhase1(msg)
		if plan.trimContent {
			kept := clipToRune(msg.Content, tieredToolTruncateChars)
			removed := len(msg.Content) - len(kept)
			msg.Content = fmt.Sprintf("%s%s%d%s", kept, tieredToolTruncationStart, removed, tieredToolTruncationEnd)
		}
		if plan.trimAttachments {
			if msg.Content != "" {
				msg.Content += "\n"
			}
			msg.Content += toolAttachmentElision
			msg.Parts = nil
		}
		out[i] = msg
	}
	return out
}

type tieredToolPhase1Plan struct {
	contentBytes    int
	trimContent     bool
	trimAttachments bool
	saved           int
}

func planTieredToolPhase1(msg llm.Message) tieredToolPhase1Plan {
	before := len(msg.Content) + llm.ContentPartsByteLen(msg.Parts)
	plan := tieredToolPhase1Plan{contentBytes: len(msg.Content)}
	if len(msg.Content) > tieredToolTruncateChars {
		keptBytes := len(clipToRune(msg.Content, tieredToolTruncateChars))
		removedBytes := len(msg.Content) - keptBytes
		candidateBytes := keptBytes + len(tieredToolTruncationStart) + decimalDigits(removedBytes) + len(tieredToolTruncationEnd)
		if candidateBytes < plan.contentBytes {
			plan.contentBytes = candidateBytes
			plan.trimContent = true
		}
	}
	partsBytes := llm.ContentPartsByteLen(msg.Parts)
	afterParts := partsBytes
	if len(msg.Parts) > 0 {
		markerBytes := len(toolAttachmentElision)
		if plan.contentBytes > 0 {
			markerBytes++
		}
		if markerBytes < partsBytes {
			plan.contentBytes += markerBytes
			afterParts = 0
			plan.trimAttachments = true
		}
	}
	plan.saved = before - plan.contentBytes - afterParts
	return plan
}

// tieredPhase2 trims assistant narrative content (the prose
// alongside tool calls). ToolCalls on the message are preserved so
// the action trail stays intact. Operates on a slice already
// produced by tieredPhase1 — does not re-truncate tool results.
func tieredPhase2(history []llm.Message, head, end int) []llm.Message {
	out := llm.CloneMessages(history)
	for i := head; i < end; i++ {
		msg := out[i]
		if msg.Role != llm.RoleAssistant || tieredAssistantSavings(msg.Content) == 0 {
			continue
		}
		prefix := clipToRune(msg.Content, tieredAssistantCapsuleEdgeChars)
		suffixStart := tieredAssistantSuffixStart(msg.Content)
		suffix := msg.Content[suffixStart:]
		removed := len(msg.Content) - len(prefix) - len(suffix)
		msg.Content = fmt.Sprintf("%s%s%d%s%s", prefix, tieredAssistantTruncationStart, removed, tieredAssistantTruncationEnd, suffix)
		out[i] = msg
	}
	return out
}

func tieredAssistantSavings(content string) int {
	if len(content) <= tieredAssistantTruncateChars {
		return 0
	}
	prefixBytes := len(clipToRune(content, tieredAssistantCapsuleEdgeChars))
	suffixStart := tieredAssistantSuffixStart(content)
	suffixBytes := len(content) - suffixStart
	removedBytes := len(content) - prefixBytes - suffixBytes
	candidateBytes := prefixBytes + len(tieredAssistantTruncationStart) + decimalDigits(removedBytes) + len(tieredAssistantTruncationEnd) + suffixBytes
	return max(len(content)-candidateBytes, 0)
}

func tieredAssistantSuffixStart(content string) int {
	suffixStart := len(content) - tieredAssistantCapsuleEdgeChars
	for suffixStart < len(content) && suffixStart > 0 && (content[suffixStart]&0xc0) == 0x80 {
		suffixStart++
	}
	return suffixStart
}

// tieredPhase3 collapses tool result bodies to a smaller single-line placeholder
// while retaining the bounded assistant operational-state capsules created by
// Phase 2. ToolCalls + ToolCallIDs remain unchanged so provider-required
// tool_call / tool_result pairs stay valid. Complete ContinuationItems are
// dropped from older messages at this explicit compaction boundary; their
// projected content and tool history remain.
func tieredPhase3(history []llm.Message, head, end int) []llm.Message {
	out := llm.CloneMessages(history)
	for i := head; i < end; i++ {
		msg := out[i]
		msg.ContinuationItems = nil
		switch msg.Role {
		case llm.RoleTool:
			placeholder := fmt.Sprintf("%s%d%s", tieredToolElisionStart, len(msg.Content), tieredToolElisionEnd)
			if len(placeholder) < len(msg.Content) {
				msg.Content = placeholder
			}
		case llm.RoleAssistant:
			// Phase 2 already reduced visible assistant prose to a bounded
			// head-and-tail capsule. Retain it so high pressure does not erase
			// decisions, resolutions, or promised next actions.
		default:
			out[i] = msg
			continue
		}
		out[i] = msg
	}
	return out
}
