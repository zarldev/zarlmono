package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// shouldRunCompact returns true when the runner's pre-iteration
// compaction hook should actually invoke the engine. Implements a
// single Prober gate: when the engine implements [compact.Prober]
// and [compact.Prober.WouldReduceBytes] returns zero (or less) on
// the current history+keep, the runner skips the Compact call
// entirely. Eliminates the per-iteration "compacted 0 bytes" noise
// that the eval traces showed (Structural fired 75 times per task,
// most of them no-ops on small histories).
//
// Engines without Prober — trackingCompactor in tests, third-party
// implementations — bypass the gate and run every iteration as
// before. This is the conservative default: an engine that hasn't
// told us how to predict its work is assumed to be doing real work
// on every call.
func shouldRunCompact(keep int, history []llm.Message, c compact.Compactor) bool {
	if p, ok := c.(compact.Prober); ok {
		return p.WouldReduceBytes(history, keep) > 0
	}
	return true
}

// maybeCompact applies the per-iteration auto-compaction policy and returns
// the (possibly trimmed) history. A non-nil error is the terminal,
// ErrCompact-wrapped compaction failure — the caller turns it into a terminal
// TaskResult. It skips iteration 0 (no prior usage to drive a decision) and
// runners without a compactor configured.
//
// Uses the unified compact.Compactor interface — the runner passes its
// configured keepRecent (a static cap set via WithCompactKeepRecent); the
// engine decides whether the current history is heavy enough to act on.
//
// Two pre-gates short-circuit before we even build the Compact call, because
// both Structural and Tiered ran (cheaply but non-trivially) every iteration
// in the eval traces — 75 fires per task, even on iterations where the
// history was nowhere near pressure:
//
//  1. Message-count floor: skip when history is at or below 2 * keep —
//     nothing older than the keep window means nothing eligible to compact.
//  2. Prober gate: if the engine implements [compact.Prober] and
//     WouldReduceBytes returns 0 for this history+keep, the engine itself
//     promises Compact would be a no-op. Skipping saves the iteration the
//     per-message scan and the publish-event branch.
//
// Both gates are conservative: an engine without Prober (or reporting a
// positive estimate) still gets called as before.
func (r *Runner) maybeCompact(ctx context.Context, spec TaskSpec, messages []llm.Message, lastUsage *llm.Usage, iterNum int, st *loopState) ([]llm.Message, error) {
	if iterNum == 0 || r.compactor == nil {
		return messages, nil
	}

	before := len(messages)
	// keepPolicy folds static / adaptive sizing and the token-pressure
	// force-path into one decision — see keep_policy.go.
	keep, forceCompact := r.keep.decide(messages, lastUsage)

	// Force-compact bypasses the Prober gate entirely (the engine has to
	// trust the runner's token-pressure read over its own byte-pressure
	// heuristic); otherwise the Prober gate decides whether to act.
	if !forceCompact && !shouldRunCompact(keep, messages, r.compactor) {
		return messages, nil
	}

	// No-op latch: a prior forced compaction freed nothing and fewer than
	// keepRecent new messages have accrued since, so there's still nothing
	// outside the keep window to trim. Skip the re-scan rather than re-run a
	// Compact we know will no-op (the eval traces showed this firing every
	// iteration on user-message-dominated histories).
	if forceCompact && st.forceCompactNoopAt > 0 && before < st.forceCompactNoopAt+keep {
		return messages, nil
	}

	result, cerr := r.compactor.Compact(ctx, messages, keep)
	if cerr != nil {
		return messages, fmt.Errorf("%w: %w", ErrCompact, cerr)
	}
	// Adopt the trimmed history whenever the engine reports work — a byte
	// trim is real even if the message count stayed the same (Structural and
	// Tiered shrink in place, never dropping messages). Gating the swap-in on
	// length alone discarded every in-place trim: the runner kept the
	// full-size history, pressure never relented, and the compactor re-fired
	// (and re-published "compacted N→N") every iteration while tokens climbed.
	// The swap-in guard MUST match the publish guard below — both keyed on
	// "did the engine do work?", not "did the count change?".
	changed := len(result.History) != before || result.BytesTrimmed > 0
	if changed {
		messages = result.History
		st.forceCompactNoopAt = 0 // real work happened — clear the no-op latch
	} else if forceCompact {
		// Forced compaction freed nothing: arm the latch so we don't re-run
		// it until keepRecent new messages have aged something out of the
		// keep window. Log once (on the transition into the latched state).
		if st.forceCompactNoopAt == 0 {
			slog.InfoContext(ctx, "runner: forced compaction freed nothing; suppressing re-runs until history grows",
				"task", spec.ID, "iter", iterNum, "messages", before, "keep", keep)
		}
		st.forceCompactNoopAt = before
	}
	if changed {
		slog.InfoContext(ctx, "runner: compacted",
			"task", spec.ID,
			"iter", iterNum,
			"engine", result.Engine,
			"messages_before", before,
			"messages_after", len(messages),
			"bytes_trimmed", result.BytesTrimmed,
		)
		r.publishCompactionApplied(ctx, spec, before, len(messages), result.BytesTrimmed, result.Engine)
	}
	return messages, nil
}
