package compact

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// AdaptiveKeepRecent returns the number of most-recent messages
// that should be kept verbatim such that their estimated token
// total stays at or under targetTokens. Walks the history tail-
// first, accumulating chars/4 estimates until the budget is hit,
// then returns the index where the keep window starts (i.e. the
// count of messages kept).
//
// Rationale: the previous default kept a fixed 4 turns. For tool-
// heavy sessions, 4 turns can be 20k+ tokens; for chatty
// narrative-only sessions, 4 turns can be 800 tokens — neither is
// the right target. Sizing by token budget means the briefing
// always has room for the kept-recent slice + the structured
// sections + the narrative, on any context window.
//
// minKeep / maxKeep clamp the returned count so a single huge
// final message (a giant tool result, paste) doesn't push us to
// keep 0, and a tiny session doesn't keep the entire history (which
// would no-op the compaction). Pass minKeep=2 / maxKeep=20 as
// sensible defaults; tune per consumer.
func AdaptiveKeepRecent(history []llm.Message, targetTokens, minKeep, maxKeep int) int {
	if len(history) == 0 || targetTokens <= 0 {
		if minKeep > 0 {
			return minKeep
		}
		return 0
	}
	if minKeep < 0 {
		minKeep = 0
	}
	if maxKeep < minKeep {
		maxKeep = minKeep
	}
	const charsPerToken = 4
	tokens := 0
	kept := 0
	for i := len(history) - 1; i >= 0; i-- {
		ch := messageChars(history[i])
		next := tokens + ch/charsPerToken
		// First message always gets in even if it pushes over —
		// otherwise minKeep clamping would do all the work and the
		// budget would only matter on the second message onwards.
		if kept > 0 && next > targetTokens {
			break
		}
		tokens = next
		kept++
		if kept >= maxKeep {
			break
		}
	}
	if kept < minKeep {
		kept = minKeep
	}
	if kept > len(history) {
		kept = len(history)
	}
	return kept
}

// messageChars sums the byte length of an llm.Message's contributing
// fields. Mirrors the shell-side msgChars but lives here so the
// compact package stays self-contained.
func messageChars(m llm.Message) int {
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	for _, p := range m.Parts {
		n += len(p.Text)
	}
	return n
}
