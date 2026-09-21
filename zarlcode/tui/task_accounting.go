package tui

import (
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// taskAccounting exists only from accepted start to terminal settlement. The
// turn owner drains child events before another root invocation may start.
type taskAccounting struct {
	depth           int
	attempt         int
	provider, model string
	price           usagePrice
}

type usagePrice struct {
	input, output float64
	known         bool
}

func (p usagePrice) cost(in, cached, out int) float64 {
	return float64(max(in-cached, 0))/1000*p.input + float64(cached)/1000*p.input*cacheReadRate + float64(out)/1000*p.output
}

func (p usagePrice) saved(cached int) float64 {
	return float64(cached) / 1000 * p.input * (1 - cacheReadRate)
}

func (s *Session) taskPrice(e teasink.ConversationStartedMsg) usagePrice {
	if s.meta != nil {
		if s.meta.IsLocal(e.Provider) || s.meta.IsSubscription(e.Provider) {
			return usagePrice{known: true}
		}
		in, out, known := s.meta.ResolveCostCached(e.Provider, e.Model)
		return usagePrice{input: in, output: out, known: known}
	}
	// Without a metadata catalogue only the explicitly configured root basis is
	// known. Never silently price a different child target as the parent.
	if e.Depth == 0 || (e.Provider == s.Provider && e.Model == s.Model) {
		return usagePrice{input: s.Run.inCostPer1k, output: s.Run.outCostPer1k,
			known: s.Run.hasPricing() || s.Run.local || s.Run.subscription}
	}
	return usagePrice{}
}

func (s *Session) applyProviderAttemptSettled(e teasink.ProviderAttemptSettledMsg) {
	task, ok := s.Run.tasks[e.TaskID]
	if !ok || task.depth != e.Depth || e.Attempt <= task.attempt {
		return
	}
	task.attempt = e.Attempt
	s.Run.tasks[e.TaskID] = task
	if s.Run.acceptsActivity(e.TaskID, e.Depth) {
		s.Run.activity.phase = ActivityPhases.ACTIVITYWORKING
	}
	if e.Depth == 0 {
		s.Run.foldUsage(e.Usage, e.Usage)
	}
}

func (s *RunState) foldUsage(u, delta *llm.Usage) {
	defer s.bumpRevision()
	if u != nil {
		s.liveCtx, s.lastIn = u.PromptTokens, u.PromptTokens
		s.liveTotal, s.lastTotal = u.TotalTokens, u.TotalTokens
		s.lastCached = u.CachedTokens
	}
	if delta != nil {
		s.lastOut = delta.CompletionTokens
		s.turnCompletionTokens += delta.CompletionTokens
	}
}

// UsageSnapshot returns an owned snapshot of this session's settled own-spend
// rollup. Inherited replay and live attempt observations are not added again.
func (m *UI) UsageSnapshot() SessionUsageSnapshot { return m.session.Run.UsageSnapshot() }
