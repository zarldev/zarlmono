// Package repeatcap provides a tiny per-Run repeat-call counter
// some "call once, the answer doesn't change" tools wrap themselves
// in to refuse the model when it starts spamming them.
//
// Use case: list_skills and list_agents are stable for the duration
// of one user-level Run. Their descriptions say "Call once early;
// they don't change mid-turn", but the LLM can ignore guidance —
// audit-style agents have been observed re-calling them dozens of
// times per turn. The cap converts that from "wasted tokens" to "a
// tool refusal the model can react to".
package repeatcap

import (
	"sync/atomic"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

// Counter is a per-(rootID, toolName) call counter backed by a
// [zsync.Map]. Multiple tools can share one Counter — the toolName
// key in HitsAndCheck disambiguates. Zero value is ready to use.
type Counter struct {
	m zsync.Map[string, *atomic.Int64] // key: rootID + "|" + toolName
}

// HitsAndCheck increments the counter for (rootID, toolName) and
// returns:
//   - hits — the new running total (1-based: first call returns 1).
//   - over — true when the new total exceeds max, telling the caller
//     to refuse this invocation rather than running it.
//
// rootID == "" is treated as "no scope" — typical for direct unit-
// test calls; the counter still increments but uses a sentinel key
// so cross-test runs don't pollute one another. max <= 0 disables
// the cap (always returns over=false).
func (c *Counter) HitsAndCheck(rootID, toolName string, maxHits int) (int64, bool) {
	key := rootID + "|" + toolName
	counter, _ := c.m.LoadOrStore(key, new(atomic.Int64))
	n := counter.Add(1)
	if maxHits <= 0 {
		return n, false
	}
	return n, n > int64(maxHits)
}
