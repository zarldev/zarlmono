package engine

import (
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func buildScopedCompactor(engine string, window int, provider llm.Provider, model string, state agentcompact.StateProvider, root string, scope inference.Scope) agentcompact.Compactor {
	compactor := buildLiveCompactor(engine, window, provider, model, state, root)
	switch c := compactor.(type) {
	case *agentcompact.Summary:
		c.InferenceScope = scope
	case *agentcompact.Executive:
		c.InferenceScope = scope
	case *agentcompact.Handover:
		c.InferenceScope = scope
	}
	return compactor
}

// InferenceSnapshot returns all scoped model consumption across members and
// descendants, including routing, decomposition judgments and compaction. It is
// not an additional task-usage event and must not be added to session task totals.
func (s *Swarm) InferenceSnapshot() inference.Snapshot { return s.goal.InferenceSnapshot() }
