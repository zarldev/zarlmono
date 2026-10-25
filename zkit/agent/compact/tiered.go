package compact

import (
	"context"
	"fmt"
	"strings"

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

// tieredAssistantTruncateChars bounds the prose retained in each host excerpt
// of an older assistant message in Phases 2 and 3.
const tieredAssistantTruncateChars = 256

const (
	tieredToolTruncationStart = "\n[truncated — "
	tieredToolTruncationEnd   = " chars elided post-compact]"
	tieredToolElisionStart    = "[tool result elided post-compact — original ~"
	tieredToolElisionEnd      = " bytes. Consult retained output; do not repeat side effects.]"
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
// Phase 1 (>= 60% budget): truncate older tool bodies to ~256 bytes and
// profitably elide their attachments. Assistant content is untouched.
//
// Phase 2 (>= 75% budget): Phase 1 + older assistant prose moved to bounded,
// host-attributed historical excerpts. Assistant tool calls remain verbatim.
// Changed prose replaces native continuations and their display reasoning;
// retaining the original native text would bypass the compacted projection.
//
// Phase 3 (>= 90% budget): Phase 2 + tool result bodies replaced
// with a single-line placeholder when that is smaller. The bounded
// head-and-tail host excerpts, ToolCalls, and ToolCallIDs remain, preserving
// operational state and provider-required tool pairing.
// As an explicit native replay boundary, complete ContinuationItems and display
// reasoning are dropped together from older messages (payloads are never truncated).
// Assistant messages with no remaining content or tool calls are removed.
//
// Below 60% budget Tiered preserves all message content and returns an owned copy.
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
// Pass 0 to fall back to TieredDefaultTargetBytes. Known small windows use
// their actual budget: clamping them to 128 KiB can prevent any compaction
// before the provider's context window is exhausted.
func NewTiered(ctxWindowTokens int) *Tiered {
	target := TieredDefaultTargetBytes
	if ctxWindowTokens > 0 {
		target = max(1, ctxWindowTokens*2)
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
	return t.plan(history, keepRecent).saved
}

// tieredPlan keeps phase selection and eligibility on the original input layout.
// Host excerpts may expand that layout only after the selected phase is known.
type tieredPlan struct {
	head, end int
	phase     int
	saved     int
}

func (t *Tiered) plan(history []llm.Message, keepRecent int) tieredPlan {
	keepRecent = max(keepRecent, 0)
	if len(history) <= keepRecent {
		return tieredPlan{}
	}
	plan := tieredPlan{end: len(history) - keepRecent}
	if history[0].Role == llm.RoleSystem {
		plan.head = 1
	}
	if plan.end <= plan.head {
		return tieredPlan{}
	}
	target := t.TargetBytes
	if target <= 0 {
		target = TieredDefaultTargetBytes
	}
	p1, p2, p3 := t.Phase1Threshold, t.Phase2Threshold, t.Phase3Threshold
	if p1 <= 0 {
		p1 = 0.60
	}
	if p2 <= 0 {
		p2 = 0.75
	}
	if p3 <= 0 {
		p3 = 0.90
	}
	var totalBytes, phase1Savings, phase2Savings, phase3Savings int
	for i, msg := range history {
		totalBytes += messageChars(msg)
		if i < plan.head || i >= plan.end {
			continue
		}
		if msg.Role == llm.RoleTool {
			tool := planTieredToolPhase1(msg)
			phase1Savings += tool.saved
			if toolRecoveryStart(msg.Content) < 0 {
				placeholderBytes := len(tieredToolElisionStart) + decimalDigits(tool.contentBytes) + len(tieredToolElisionEnd)
				phase3Savings += max(tool.contentBytes-placeholderBytes, 0)
			}
		}
		var nativeBytes int
		for _, item := range msg.ContinuationItems {
			nativeBytes += item.ByteLen()
		}
		if msg.Role == llm.RoleAssistant {
			if saved := tieredAssistantSavings(msg.Content); saved > 0 {
				phase2Savings += saved + nativeBytes + len(msg.ReasoningContent)
				continue // Phase 2 already establishes the projected replay boundary.
			}
			phase3Savings += len(msg.ReasoningContent)
		}
		phase3Savings += nativeBytes
	}
	if totalBytes < int(float64(target)*p1) {
		return tieredPlan{}
	}
	plan.phase, plan.saved = 1, phase1Savings
	if totalBytes-plan.saved < int(float64(target)*p2) {
		return plan
	}
	plan.phase, plan.saved = 2, plan.saved+phase2Savings
	if totalBytes-plan.saved < int(float64(target)*p3) {
		return plan
	}
	plan.phase, plan.saved = 3, plan.saved+phase3Savings
	return plan
}

// Compact implements [Compactor].
func (t *Tiered) Compact(_ context.Context, history []llm.Message, keepRecent int) (Result, error) {
	plan := t.plan(history, keepRecent)
	if plan.phase == 0 {
		return Result{History: llm.CloneMessages(history), Engine: EngineTiered}, nil
	}
	out := make([]llm.Message, 0, len(history))
	for i, original := range history {
		msg := original.Clone()
		if i < plan.head || i >= plan.end {
			out = append(out, msg)
			continue
		}
		if msg.Role == llm.RoleTool {
			msg = tieredTrimTool(msg)
		}
		if plan.phase >= 2 && msg.Role == llm.RoleAssistant && tieredAssistantSavings(msg.Content) > 0 {
			out = append(out, tieredAssistantExcerpt(msg.Content))
			// Excerpts are host input, not fabricated assistant output. Keep only
			// the assistant's action/attachment shell at this replay boundary.
			msg.Content = ""
			msg.ContentOutputIndex = nil
			msg.ReasoningContent = ""
			msg.ContinuationItems = nil
			if len(msg.ToolCalls) == 0 && len(msg.Parts) == 0 {
				continue
			}
		}
		if plan.phase == 3 {
			msg.ContinuationItems = nil
			switch msg.Role {
			case llm.RoleTool:
				placeholder := fmt.Sprintf("%s%d%s", tieredToolElisionStart, len(msg.Content), tieredToolElisionEnd)
				if len(placeholder) < len(msg.Content) && toolRecoveryStart(original.Content) < 0 {
					msg.Content = placeholder
				}
			case llm.RoleAssistant:
				msg.ReasoningContent = ""
				if msg.Content == "" {
					msg.ContentOutputIndex = nil
					if len(msg.ToolCalls) == 0 && len(msg.Parts) == 0 {
						continue
					}
				}
			}
		}
		out = append(out, msg)
	}
	return tieredResult(out, plan.saved, plan.phase), nil
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

// tieredTrimTool truncates a cloned tool message's body and elides profitable
// attachments. Its plan supplies the same byte accounting used by the probe.
func tieredTrimTool(msg llm.Message) llm.Message {
	plan := planTieredToolPhase1(msg)
	if plan.trimContent {
		head, tail := tieredToolEdges(msg.Content)
		removed := len(msg.Content) - len(head) - len(tail)
		msg.Content = fmt.Sprintf("%s%s%d%s%s", head, tieredToolTruncationStart, removed, tieredToolTruncationEnd, tail)
	}
	if plan.trimAttachments {
		if msg.Content != "" {
			msg.Content += "\n"
		}
		msg.Content += toolAttachmentElision
		msg.Parts = nil
	}
	return msg
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
		head, tail := tieredToolEdges(msg.Content)
		keptBytes := len(head) + len(tail)
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

func tieredAssistantSavings(content string) int {
	if len(content) <= tieredAssistantTruncateChars {
		return 0
	}
	prefixBytes := len(clipToRune(content, tieredAssistantCapsuleEdgeChars))
	suffixBytes := len(content) - tieredAssistantSuffixStart(content)
	candidateBytes := len(tieredExcerptStart) + prefixBytes + len(tieredExcerptEnd) + suffixBytes
	return max(len(content)-candidateBytes, 0)
}

func tieredAssistantSuffixStart(content string) int {
	suffixStart := len(content) - tieredAssistantCapsuleEdgeChars
	for suffixStart < len(content) && suffixStart > 0 && (content[suffixStart]&0xc0) == 0x80 {
		suffixStart++
	}
	return suffixStart
}

// Preserve a bounded existing spill footer or program reader recipe as reference
// text, never as proof of retention or authority. Receipts may expire or be revoked;
// preserving their text neither extends their lifetime nor grants reader access.
func toolRecoveryStart(content string) int {
	start := -1
	for _, marker := range []string{"\n\n[truncated by ", "Read with program_output_read(receipt="} {
		candidate := strings.LastIndex(content, marker)
		if candidate >= 0 && len(content)-candidate <= 2048 && (start < 0 || candidate < start) {
			start = candidate
		}
	}
	return start
}

func tieredToolEdges(content string) (string, string) {
	head := clipToRune(content, tieredToolTruncateChars/2)
	tail := tieredAssistantSuffixStart(content)
	if recovery := toolRecoveryStart(content); recovery >= 0 {
		tail = min(tail, recovery)
	}
	// A footer can begin inside the head for a short result. Keep the result
	// unchanged rather than duplicate overlapping bytes.
	tail = max(tail, len(head))
	return head, content[tail:]
}
