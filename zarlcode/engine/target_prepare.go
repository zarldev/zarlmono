package engine

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

const targetModelInfoTTL = 5 * time.Minute

// PrepareTarget builds a provider and resolves its operational budget with the
// existing Settings.ContextWindow policy, including account/local probes. It
// creates a new generation and optional empty gateway cache, but does not fetch
// model-info enrichment. Denied enrichment cannot gate provider construction.
// Spec.Protocol remains override intent; Facts.SelectedProtocol is actual codec
// selection only when the constructed adapter proves it.
func (s *Settings) PrepareTarget(ctx context.Context, spec ProviderSpec) (TargetUpdate, error) {
	return s.prepareTarget(ctx, spec, s.ContextWindow)
}

// PrepareTargetCached builds a provider using only the process-local budget
// resolver. It adds no metadata, disk-cache or probe I/O to provider construction
// and is suitable for interactive first render. Credential resolution retains
// BuildProvider's existing behavior. Later probes/refreshes are caller-owned.
func (s *Settings) PrepareTargetCached(ctx context.Context, spec ProviderSpec) (TargetUpdate, error) {
	return s.prepareTarget(ctx, spec, func(_ context.Context, resolved ProviderSpec) int {
		return s.Registry.ResolveContextWindowCached(resolved.Name, resolved.Model)
	})
}

func (s *Settings) prepareTarget(ctx context.Context, spec ProviderSpec, window func(context.Context, ProviderSpec) int) (TargetUpdate, error) {
	built, err := buildProviderResolved(ctx, s.Registry, s.Svc, spec, spec.CachePolicy)
	if err != nil {
		return TargetUpdate{}, err
	}
	provider := built.Provider
	if built.Model != "" {
		spec.Model = built.Model
	}
	tokens := window(ctx, spec)
	source := OperationalBudgetSources.BUDGETOPAQUELEGACYRESOLUTION
	if tokens <= 0 {
		tokens, source = LiveContextWindow, OperationalBudgetSources.BUDGETAPPLICATIONFALLBACK
	} else if def, err := s.Registry.Parse(spec.Name); err == nil && def.ContextWindow > 0 {
		source = OperationalBudgetSources.BUDGETEXPLICITCONFIGURATION
	}
	scope := &targetScope{}
	protocol := built.Protocol
	if gateway, ok := unwrapTargetProvider(provider).(*litellm.Provider); ok {
		scope.models = litellm.NewModelSource(gateway, targetModelInfoTTL)
		scope.provider = gateway
		scope.selector = RateCardTarget{
			Provider: spec.Name, Model: llm.ModelID(spec.Model),
			BaseURL: spec.BaseURL, Protocol: protocol,
		}
	}
	// A selected codec is not a complete client capability assertion. No
	// exhaustive candidates, canonical identity or price mapping is available.
	resolution := s.Registry.ResolveModelCached(backends.ModelBinding{
		Route: llm.RouteID(spec.Name), RequestedModel: llm.ModelID(spec.Model),
	}, time.Now())
	// Catalogue and gateway observations are not an explicit local policy.
	resolution.Descriptor.RateCard = llm.RateCardReference{}
	return TargetUpdate{Provider: provider, Spec: spec, Window: tokens, Facts: &TargetFacts{
		Generation: TargetGeneration{owner: scope}, RequestedAlias: llm.ModelID(spec.Model),
		SelectedProtocol: protocol, CachePolicy: built.CachePolicy, Resolution: resolution,
		Budget: OperationalBudget{Tokens: tokens, Source: source},
	}}, nil
}

func unwrapTargetProvider(provider llm.Provider) llm.Provider {
	for {
		wrapper, ok := provider.(interface{ Unwrap() llm.Provider })
		if !ok {
			return provider
		}
		provider = wrapper.Unwrap()
	}
}
