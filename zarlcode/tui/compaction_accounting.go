package tui

import "github.com/zarldev/zarlmono/zarlcode/tui/teasink"

func (s *Session) applyCompactionSettled(e teasink.CompactionSettledMsg) {
	// A compactor is a separate inference target, even during a root turn.
	// Depth 1 prevents taskPrice from borrowing the root's rates for it.
	inference := e.Inference
	price := s.taskPrice(teasink.ConversationStartedMsg{Depth: 1, Provider: inference.Provider, Model: inference.Model})
	s.Run.foldCacheTarget(price, inference.Usage, false, true)
	s.Run.foldAuxiliaryUsage(inference.Usage, price)
	key := cacheTargetKey{provider: price.provider, model: price.model, billingClass: price.billingClass()}
	d := s.Run.cacheTargets[key]
	d.CompactorDurationNanos += int64(inference.Duration)
	s.Run.cacheTargets[key] = d
	s.Run.bumpRevision()
}
