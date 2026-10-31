package swarm

import (
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/options"
)

// WithInferenceAccounting replaces activation-result token sums with a shared
// account. Executors must pass Activation.InferenceScope to every model caller,
// including descendants and auxiliary inference. Engine composition does this
// automatically; standalone hosts opting in own that wiring responsibility.
func WithInferenceAccounting() options.Option[Swarm] { return func(s *Swarm) { s.accounted = true } }

// InferenceScope lends the goal account to this member's activation and children.
// It is disabled unless the goal enables WithInferenceAccounting.
func (a Activation) InferenceScope() inference.Scope {
	return inference.Scope{Account: a.Member.owner.account, Member: string(a.Member.id)}
}

// InferenceSnapshot returns goal-wide scoped consumption, not additional task
// usage. A disabled account returns a zero snapshot, not legacy usage totals.
func (s *Swarm) InferenceSnapshot() inference.Snapshot {
	if !s.accounted {
		return inference.Snapshot{}
	}
	return s.account.Snapshot()
}
