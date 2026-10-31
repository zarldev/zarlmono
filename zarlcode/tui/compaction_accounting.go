package tui

import "github.com/zarldev/zarlmono/zarlcode/tui/teasink"

func (s *Session) applyCompactionSettled(e teasink.CompactionSettledMsg) {
	inference := e.Inference
	// A compactor is a separate inference target and cannot borrow root pricing.
	price := s.taskPrice(teasink.ConversationStartedMsg{Depth: 1, Provider: inference.Provider, Model: inference.Model})
	s.Run.foldCacheTarget(price, inference.Usage, false, true)
	s.Run.foldAuxiliaryUsage(inference.Usage, price)
	key := cacheTargetKey{provider: price.provider, model: price.model, billingClass: price.billingClass()}
	d := s.Run.cacheTargets[key]
	d.CompactorDurationNanos += int64(inference.Duration)
	s.Run.cacheTargets[key] = d
	s.Run.bumpRevision()
}
