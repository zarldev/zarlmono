package engine

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

// BindTargetRateCard selects an explicit local estimate policy for this exact
// prepared construction, returning independently owned facts and rates. It does
// not mutate update or config, perform I/O, or infer pricing from observations.
// Applicability is an operator assertion, not verified invoice coverage.
//
// First use supports actual LiteLLM selected protocols only. Legacy/zero facts,
// other codecs with unknown actual protocol, and a changed connection, alias or
// provider handle return ErrRateCardTargetMismatch. Spec.Protocol is override
// intent, not the protocol used for selection. Empty BaseURL matches a prepared
// registered default with no runtime override; it is never a wildcard. The
// private selector belongs to the built provider, not a future registry lookup.
// A new preparation must be explicitly bound again, even for identical text.
func BindTargetRateCard(update TargetUpdate, config RateCardConfig) (TargetUpdate, error) {
	if update.Facts == nil || update.Facts.Generation.owner == nil {
		return TargetUpdate{}, ErrRateCardTargetMismatch
	}
	scope := update.Facts.Generation.owner
	provider, ok := unwrapTargetProvider(update.Provider).(*litellm.Provider)
	if !ok || scope.provider == nil || provider != scope.provider ||
		scope.selector.Provider != update.Spec.Name ||
		scope.selector.Model != llm.ModelID(update.Spec.Model) ||
		scope.selector.BaseURL != update.Spec.BaseURL ||
		scope.selector.Model != update.Facts.RequestedAlias ||
		scope.selector.Protocol == llm.ModelProtocols.PROTOCOLUNKNOWN ||
		scope.selector.Protocol != update.Facts.SelectedProtocol ||
		scope.selector.Protocol != provider.Protocol() {
		return TargetUpdate{}, ErrRateCardTargetMismatch
	}
	card, err := config.Select(scope.selector)
	if err != nil {
		return TargetUpdate{}, err
	}
	update.Facts, update.RateCard = cloneTargetBasis(update.Facts, &card)
	update.Facts.Resolution.Descriptor.RateCard = card.Reference
	return update, nil
}

// cloneTargetBasis keeps the pure reference consistent with the optional frozen
// policy. An update without a card clears any old reference, including when a
// caller retained facts from a previously bound preparation.
func cloneTargetBasis(facts *TargetFacts, card *llm.RateCard) (*TargetFacts, *llm.RateCard) {
	facts = cloneTargetFacts(facts)
	var owned *llm.RateCard
	if card != nil {
		clone := card.Clone()
		owned = &clone
	}
	if facts != nil && (owned == nil || facts.Resolution.Descriptor.RateCard != owned.Reference) {
		facts.Resolution.Descriptor.RateCard = llm.RateCardReference{}
	}
	return facts, owned
}
