package engine

import (
	"context"
	"errors"

	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

// ErrTargetModelInfoUnavailable means this construction has no LiteLLM metadata
// source. It differs from litellm.ErrModelInfoNotCached (not refreshed yet) and
// litellm.ErrModelInfoUnavailable (optional enrichment denied or not found).
var ErrTargetModelInfoUnavailable = errors.New("target has no model info source")

// RefreshModelInfo explicitly refreshes this construction's optional gateway
// enrichment with the caller's context. Returned rows are independently owned,
// gateway-declared observations, not effective capabilities or applicable rates.
// Errors preserve the source's identities and leave any last-good cache intact.
// A completion for an old generation cannot update any newer preparation.
func (f TargetFacts) RefreshModelInfo(ctx context.Context) (litellm.ModelInfoSnapshot, error) {
	if f.Generation.owner == nil || f.Generation.owner.models == nil {
		return litellm.ModelInfoSnapshot{}, ErrTargetModelInfoUnavailable
	}
	return f.Generation.owner.models.Refresh(ctx)
}

// CachedModelInfo returns an independently owned last-good observation without
// I/O or expiry extension. Callers compare its expiry against their own clock;
// stale observations remain available. This never changes Resolution or Budget.
func (f TargetFacts) CachedModelInfo() (litellm.ModelInfoSnapshot, error) {
	if f.Generation.owner == nil || f.Generation.owner.models == nil {
		return litellm.ModelInfoSnapshot{}, ErrTargetModelInfoUnavailable
	}
	return f.Generation.owner.models.Snapshot()
}
