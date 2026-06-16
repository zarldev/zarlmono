package runner

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// keepPolicy computes the keepRecent argument the runner hands the
// compactor each iteration — the number of most-recent messages the
// engine must preserve verbatim — and whether to force a Compact. It
// folds the three previously-scattered sizing mechanisms into one place
// so "which knob wins" is decided here rather than across the struct,
// the options, and maybeCompact (a split that had already let the
// force-path keep value drift from its documentation).
//
// Precedence, applied in decide:
//   - base: adaptive (token-budget-aware) when set, else the static
//     floor. WithCompactKeepRecent sets static; WithAdaptiveKeepRecent
//     sets adaptive; they are mutually exclusive (last-write-wins).
//   - pressure override: when the provider's reported prompt tokens
//     cross fraction × budget, force a trim and shrink keep to 1.
type keepPolicy struct {
	static   int                     // floor used when adaptive is nil
	adaptive func([]llm.Message) int // token-budget-aware sizing; wins over static
	budget   int                     // token-pressure budget in tokens; 0 disables the force-path
	fraction float64                 // ratio of budget that triggers a forced trim
}

// decide returns the keepRecent count for this iteration and whether the
// runner should force a Compact (bypassing the engine's Prober gate).
// lastUsage is the previous turn's provider-reported usage; nil (or a
// disabled budget) means the pressure override never fires.
func (p keepPolicy) decide(messages []llm.Message, lastUsage *llm.Usage) (int, bool) {
	keep := p.static
	if p.adaptive != nil {
		keep = p.adaptive(messages)
	}

	// Token-pressure force-path. Engine-side byte heuristics (Tiered's
	// TargetBytes thresholds, Structural's per-message cutoffs) can
	// under-detect real context pressure: a 35B-class local model loses
	// structured-output discipline around 25–35% of its nominal window,
	// long before any byte estimate alarms. The provider's tokenizer is
	// the only reliable signal we've crossed the *effective* coherent
	// window, so when it does we trust it over the engine.
	if p.budget > 0 && p.fraction > 0 && lastUsage != nil {
		used := lastUsage.PromptTokens
		if used == 0 {
			used = lastUsage.TotalTokens
		}
		if used > 0 && float64(used)/float64(p.budget) >= p.fraction {
			// Shrink keep to 1 — just the latest message survives — so the
			// latest (often huge) tool result is itself eligible for
			// Tiered's Phase-3 placeholdering next iteration. Without this
			// the loop treads water: the engine trims old context while a
			// fresh tool result inside the keep window adds equivalent
			// bytes, leaving net prompt size unchanged. The assistant
			// tool_call metadata pointing at the result survives Phase 3,
			// so the model still sees what it asked for.
			if keep > 1 {
				keep = 1
			}
			return keep, true
		}
	}
	return keep, false
}
