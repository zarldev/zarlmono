package engine

import (
	"slices"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

// TargetGeneration is a comparable, process-local construction identity. Its
// zero value is absent. It contains no credential, key hash, or serializable ID;
// equality, not formatting or persistence, associates later enrichment with a
// preparation. Repeating even an identical spec creates a different generation.
type TargetGeneration struct {
	owner *targetScope
}

// targetScope is non-zero-sized even without enrichment. Its address supplies
// identity without a global counter. The optional cache belongs to the actual
// built provider, including its credential scope, and starts no workers. The
// non-secret selector and borrowed provider handle freeze proof for local policy
// binding; no credential or key hash is copied into the selector.
type targetScope struct {
	models   *litellm.ModelSource
	provider *litellm.Provider
	selector RateCardTarget
}

// OperationalBudget is the compaction budget, not an advertised model context
// ceiling. Source does not claim which legacy table, probe or catalogue won.
type OperationalBudget struct {
	Tokens int
	Source OperationalBudgetSource
}

// TargetFacts describes one actual provider construction. Resolution remains
// incomplete in the absence of explicit model/deployment/client bindings; gateway
// declarations never grant capabilities or select an applicable rate card.
//
// Collections are caller-owned; value copies borrow them. Clone isolates pure
// facts but intentionally shares the private generation's optional synchronized
// metadata cache. Refreshes change only that cache, never these pure facts or a
// later preparation's cache. The provider must not be reconfigured after build.
// RequestedAlias is the resolved configured alias; empty means the constructor
// uses an unobservable private default, not a proven canonical identity.
type TargetFacts struct {
	Generation       TargetGeneration
	RequestedAlias   llm.ModelID
	SelectedProtocol llm.ModelProtocol
	Resolution       backends.ModelResolution
	Budget           OperationalBudget
	// CachePolicy is the effective policy of the actual constructed provider.
	CachePolicy backends.RouteCachePolicy
}

// Clone returns independently owned pure facts while preserving construction
// identity and its optional metadata source.
func (f TargetFacts) Clone() TargetFacts {
	f.Resolution.Descriptor = f.Resolution.Descriptor.Clone()
	f.Resolution.Issues = slices.Clone(f.Resolution.Issues)
	for i := range f.Resolution.Issues {
		f.Resolution.Issues[i].Evidence = slices.Clone(f.Resolution.Issues[i].Evidence)
	}
	return f
}

func cloneTargetFacts(facts *TargetFacts) *TargetFacts {
	if facts == nil {
		return nil
	}
	clone := facts.Clone()
	return &clone
}

// Clone returns a snapshot with independently owned pure facts and rate card. Provider and
// tool handles remain borrowed; metadata cache identity remains generation-local.
func (t RunTarget) Clone() RunTarget {
	t.Facts, t.RateCard = cloneTargetBasis(t.Facts, t.RateCard)
	return t
}
