package engine

import (
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
)

func buildScopedCompactor(engine string, window int, target auxiliaryTarget, model string, state agentcompact.StateProvider, root string, scope inference.Scope, auxiliary auxiliaryProviderFactory) agentcompact.Compactor {
	compactor := buildLiveCompactor(engine, window, target.provider, model, state, root)
	switch c := compactor.(type) {
	case *agentcompact.Summary:
		c.InferenceScope = scope
		c.Provider = auxiliary.bind(AuxiliaryAccountingPurposes.PURPOSESUMMARY, target)
	case *agentcompact.Executive:
		c.InferenceScope = scope
		c.Provider = auxiliary.bind(AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, target)
	case *agentcompact.Handover:
		c.InferenceScope = scope
		c.Provider = auxiliary.bind(AuxiliaryAccountingPurposes.PURPOSEHANDOVER, target)
	}
	return compactor
}

// InferenceSnapshot returns all scoped model consumption across members and
// descendants, including routing, decomposition judgments and compaction. It is
// not an additional task-usage event and must not be added to session task totals.
func (s *Swarm) InferenceSnapshot() inference.Snapshot { return s.goal.InferenceSnapshot() }
