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
	iterationSeen   bool
	iteration       int
	provider, model string
	price           usagePrice
}

type usagePrice struct {
	provider, model     string
	input, output       float64
	inputKnown          bool
	outputKnown         bool
	metered             bool
	local, subscription bool
	tierSensitive       bool
}

func (p usagePrice) cost(in, cached, created, out int) float64 {
	fresh := max(in-cached-created, 0)
	var cost float64
	if p.inputKnown {
		cost += float64(fresh) / 1000 * p.input
	}
	if p.outputKnown {
		cost += float64(out) / 1000 * p.output
	}
	return cost
}

func (p usagePrice) missingBaseRate(u *llm.Usage) bool {
	if !p.metered || u == nil {
		return false
	}
	fresh := max(u.PromptTokens-u.CachedTokens-u.CacheCreationTokens, 0)
	return (fresh > 0 && !p.inputKnown) || (u.CompletionTokens > 0 && !p.outputKnown)
}

func (s *Session) taskPrice(e teasink.ConversationStartedMsg) usagePrice {
	price := usagePrice{provider: e.Provider, model: e.Model}
	if s.meta != nil {
		price.local = s.meta.IsLocal(e.Provider)
		price.subscription = s.meta.IsSubscription(e.Provider)
		if price.local || price.subscription {
			return price
		}
		price.input, price.output, _ = s.meta.ResolveCostCached(e.Provider, e.Model)
		price.inputKnown = price.input > 0
		price.outputKnown = price.output > 0
		price.metered = true
		price.tierSensitive = promptTierSensitive(e.Model)
		return price
	}
	// Without a metadata catalogue only the explicitly configured root basis is
	// known. Never silently price a different child target as the parent.
	if e.Depth == 0 || (e.Provider == s.Provider && e.Model == s.Model) {
		price.input, price.output = s.Run.inCostPer1k, s.Run.outCostPer1k
		price.inputKnown = price.input > 0
		price.outputKnown = price.output > 0
		price.metered = !s.Run.local && !s.Run.subscription
		price.local, price.subscription = s.Run.local, s.Run.subscription
		price.tierSensitive = price.metered && promptTierSensitive(e.Model)
		return price
	}
	price.metered = true
	return price
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
		s.Run.observeAttemptTiming(e)
	}
}

func (s *RunState) foldUsage(u, delta *llm.Usage) {
	defer s.bumpRevision()
	if u != nil {
		s.liveCtx, s.lastIn = u.PromptTokens, u.PromptTokens
		s.liveTotal, s.lastTotal = u.TotalTokens, u.TotalTokens
		s.lastCached = u.CachedTokens
		s.lastCacheCreation = u.CacheCreationTokens
		s.lastCacheCreation5m = u.CacheCreation5mTokens
		s.lastCacheCreation1h = u.CacheCreation1hTokens
	}
	if delta != nil {
		s.lastOut = delta.CompletionTokens
		s.turnCompletionTokens += delta.CompletionTokens
	}
}

// UsageSnapshot returns an owned snapshot of this session's settled own-spend
// rollup. Inherited replay and live attempt observations are not added again.
func (m *UI) UsageSnapshot() SessionUsageSnapshot { return m.session.Run.UsageSnapshot() }

// CacheDiagnostics returns a sorted, independently owned snapshot of terminal
// usage attributed to physical provider/model targets.
func (m *UI) CacheDiagnostics() []CacheTargetDiagnostics {
	return cloneCacheTargets(sortedCacheTargets(m.session.Run.cacheTargets))
}
