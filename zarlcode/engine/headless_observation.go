package engine

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// HeadlessProviderObservation is an owned JSON snapshot of one provider attempt.
// Nil sidecars mean absent evidence; present zero values retain unknown fields.
// LegacyUsage does not imply that NormalizedPricingUsage was supplied.
type HeadlessProviderObservation struct {
	Attempt                int                   `json:"attempt"`
	LegacyUsage            *HeadlessUsage        `json:"legacy_usage"`
	NormalizedPricingUsage *HeadlessPricingUsage `json:"normalized_pricing_usage"`
	Execution              *HeadlessExecution    `json:"execution"`
	// RateCard is the host-selected basis frozen before this invocation.
	RateCard *HeadlessRateCard `json:"rate_card"`
	// ConfiguredEstimate is separate from reported cost, never a billing total.
	// It is nil unless both RateCard and normalized pricing usage were supplied.
	ConfiguredEstimate *HeadlessPriceEstimate `json:"configured_estimate"`
	// ConfiguredEstimateUnavailable explains missing normalized usage when a
	// basis exists. It is a diagnostic, not a pricing issue or fabricated estimate.
	ConfiguredEstimateUnavailable string `json:"configured_estimate_unavailable"`
}

// HeadlessRateCardReference identifies the exact configured entry and revision.
type HeadlessRateCardReference struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Entry    string `json:"entry"`
}

// HeadlessPricingProvenance keeps source revision and freshness separate from
// the operator's card revision. Zero times mean absent timestamp evidence.
type HeadlessPricingProvenance struct {
	Source     string    `json:"source"`
	Snapshot   string    `json:"snapshot"`
	Revision   string    `json:"revision"`
	ObservedAt time.Time `json:"observed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// HeadlessTokenRate preserves unknown versus known zero, in card currency per
// million tokens. No currency or unit conversion is implied.
type HeadlessTokenRate struct {
	PerMillionTokens float64 `json:"per_million_tokens"`
	Known            bool    `json:"known"`
}

// HeadlessCacheWriteRate prices exactly LifetimeNS nanoseconds of cache lifetime.
type HeadlessCacheWriteRate struct {
	LifetimeNS int64             `json:"lifetime_ns"`
	Rate       HeadlessTokenRate `json:"rate"`
}

// HeadlessRateCard is an owned projection of the configured attempt basis.
// Applicable and TokenOnly are host policy assertions, not invoice guarantees.
type HeadlessRateCard struct {
	Reference       HeadlessRateCardReference `json:"reference"`
	PricingProvider string                    `json:"pricing_provider"`
	Currency        string                    `json:"currency"`
	Billing         string                    `json:"billing"`
	Provenance      HeadlessPricingProvenance `json:"provenance"`
	Applicable      bool                      `json:"applicable"`
	TokenOnly       bool                      `json:"token_only"`
	FreshInput      HeadlessTokenRate         `json:"fresh_input"`
	CacheRead       HeadlessTokenRate         `json:"cache_read"`
	Output          HeadlessTokenRate         `json:"output"`
	CacheWrites     []HeadlessCacheWriteRate  `json:"cache_writes"`
}

// HeadlessPricingIssue describes omitted coverage without inventing quantities.
type HeadlessPricingIssue struct {
	Kind       string `json:"kind"`
	Field      string `json:"field"`
	LifetimeNS int64  `json:"lifetime_ns"`
}

// HeadlessPriceEstimate is a configured token estimate, not reported cost or an
// invoice. Amounts are in Currency; HasKnownSubtotal preserves known zero versus
// unknown. Complete asserts coverage only as defined by the selected card and
// normalized usage. Issues explain incomplete or unavailable numeric coverage.
type HeadlessPriceEstimate struct {
	Reference        HeadlessRateCardReference `json:"reference"`
	PricingProvider  string                    `json:"pricing_provider"`
	Currency         string                    `json:"currency"`
	Billing          string                    `json:"billing"`
	Provenance       HeadlessPricingProvenance `json:"provenance"`
	InputAmount      float64                   `json:"input_amount"`
	OutputAmount     float64                   `json:"output_amount"`
	Subtotal         float64                   `json:"subtotal"`
	HasKnownSubtotal bool                      `json:"has_known_subtotal"`
	Complete         bool                      `json:"complete"`
	Issues           []HeadlessPricingIssue    `json:"issues"`
}

// HeadlessTokenQuantity preserves both token units and known-zero presence.
type HeadlessTokenQuantity struct {
	Tokens int64 `json:"tokens"`
	Known  bool  `json:"known"`
}

// HeadlessCacheWriteQuantity is a cache-write subset with an explicit nanosecond
// lifetime. Zero lifetime means unknown; it is not a default cache lifetime.
type HeadlessCacheWriteQuantity struct {
	LifetimeNS int64                 `json:"lifetime_ns"`
	Quantity   HeadlessTokenQuantity `json:"quantity"`
}

// HeadlessPricingInput retains inclusive input and its disjoint cache subsets.
// WritesByLifetime partitions CacheWrite rather than adding billable tokens.
type HeadlessPricingInput struct {
	Total            HeadlessTokenQuantity        `json:"total"`
	CacheRead        HeadlessTokenQuantity        `json:"cache_read"`
	CacheWrite       HeadlessTokenQuantity        `json:"cache_write"`
	Other            HeadlessTokenQuantity        `json:"other"`
	WritesByLifetime []HeadlessCacheWriteQuantity `json:"writes_by_lifetime"`
	PartitionKnown   bool                         `json:"partition_known"`
}

// HeadlessPricingOutput retains inclusive output and its unsupported subset.
type HeadlessPricingOutput struct {
	Total HeadlessTokenQuantity `json:"total"`
	Other HeadlessTokenQuantity `json:"other"`
}

// HeadlessPricingUsage retains normalized provider evidence without deriving it
// from legacy usage or estimating charges.
type HeadlessPricingUsage struct {
	Input                     HeadlessPricingInput  `json:"input"`
	Output                    HeadlessPricingOutput `json:"output"`
	AdditionalChargesPossible bool                  `json:"additional_charges_possible"`
}

// HeadlessObservedCount distinguishes unknown retry/fallback counts from zero.
type HeadlessObservedCount struct {
	Count int64 `json:"count"`
	Known bool  `json:"known"`
}

// HeadlessReportedCost retains reported cost, including zero, separately from
// estimates. Empty currency and false coverage_known remain unknown; no currency
// is inferred and this amount must not be added to estimates of the same usage.
type HeadlessReportedCost struct {
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Reported      bool    `json:"reported"`
	CoverageKnown bool    `json:"coverage_known"`
	Source        string  `json:"source"`
}

// HeadlessExecution is observed execution evidence, not canonical model identity,
// placement enforcement or a selected pricing basis. Request alias, deployment,
// reported model/version and gateway software version remain separate fields.
type HeadlessExecution struct {
	Route                  string                `json:"route"`
	RequestedModel         string                `json:"requested_model"`
	GatewayDeployment      string                `json:"gateway_deployment"`
	GatewayVersion         string                `json:"gateway_version"`
	ReportedModel          string                `json:"reported_model"`
	ReportedVersion        string                `json:"reported_version"`
	GatewayCallID          string                `json:"gateway_call_id"`
	ProviderCallID         string                `json:"provider_call_id"`
	PromptCacheReadTokens  HeadlessTokenQuantity `json:"prompt_cache_read_tokens"`
	PromptCacheWriteTokens HeadlessTokenQuantity `json:"prompt_cache_write_tokens"`
	RetryCount             HeadlessObservedCount `json:"retry_count"`
	FallbackCount          HeadlessObservedCount `json:"fallback_count"`
	AffinitySupport        string                `json:"affinity_support"`
	ReportedCost           HeadlessReportedCost  `json:"reported_cost"`
}

func projectProviderObservations(observations []runner.ProviderAttemptObservation) []HeadlessProviderObservation {
	if observations == nil {
		return nil
	}
	projected := make([]HeadlessProviderObservation, len(observations))
	for i, observation := range observations {
		out := HeadlessProviderObservation{Attempt: observation.Attempt}
		if card := observation.RateCard; card != nil {
			out.RateCard = projectRateCard(*card)
			if observation.PricingUsage != nil {
				out.ConfiguredEstimate = projectPriceEstimate(backends.EstimatePrice(*card, *observation.PricingUsage))
			} else {
				out.ConfiguredEstimateUnavailable = "missing normalized pricing usage"
			}
		}
		out.LegacyUsage = projectLegacyUsage(observation.Usage)
		out.NormalizedPricingUsage = projectPricingUsage(observation.PricingUsage)
		out.Execution = projectExecution(observation.Execution)
		projected[i] = out
	}
	return projected
}

func projectLegacyUsage(u *llm.Usage) *HeadlessUsage {
	if u == nil {
		return nil
	}
	return &HeadlessUsage{
		PromptTokens: u.PromptTokens, CachedTokens: u.CachedTokens,
		CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
		CacheCreationTokens: u.CacheCreationTokens, CacheCreation5mTokens: u.CacheCreation5mTokens,
		CacheCreation1hTokens: u.CacheCreation1hTokens,
		ReasoningTokens:       u.ReasoningTokens, ReasoningTokensReported: u.ReasoningTokensReported,
	}
}

func projectPricingUsage(u *llm.PricingUsage) *HeadlessPricingUsage {
	if u == nil {
		return nil
	}
	p := &HeadlessPricingUsage{
		Input: HeadlessPricingInput{
			Total: HeadlessTokenQuantity(u.Input.Total), CacheRead: HeadlessTokenQuantity(u.Input.CacheRead),
			CacheWrite: HeadlessTokenQuantity(u.Input.CacheWrite), Other: HeadlessTokenQuantity(u.Input.Other),
			PartitionKnown: u.Input.PartitionKnown,
		},
		Output: HeadlessPricingOutput{
			Total: HeadlessTokenQuantity(u.Output.Total), Other: HeadlessTokenQuantity(u.Output.Other),
		},
		AdditionalChargesPossible: u.AdditionalChargesPossible,
	}
	if u.Input.WritesByLifetime != nil {
		p.Input.WritesByLifetime = make([]HeadlessCacheWriteQuantity, len(u.Input.WritesByLifetime))
		for j, write := range u.Input.WritesByLifetime {
			p.Input.WritesByLifetime[j] = HeadlessCacheWriteQuantity{
				LifetimeNS: int64(write.Lifetime), Quantity: HeadlessTokenQuantity(write.Quantity),
			}
		}
	}
	return p
}

func projectExecution(e *llm.ExecutionObservation) *HeadlessExecution {
	if e == nil {
		return nil
	}
	return &HeadlessExecution{
		Route: string(e.Route), RequestedModel: string(e.RequestedModel),
		GatewayDeployment: string(e.GatewayDeployment), GatewayVersion: string(e.GatewayVersion),
		ReportedModel: string(e.ReportedModel), ReportedVersion: string(e.ReportedVersion),
		GatewayCallID: string(e.GatewayCallID), ProviderCallID: string(e.ProviderCallID),
		PromptCacheReadTokens:  HeadlessTokenQuantity(e.PromptCacheReadTokens),
		PromptCacheWriteTokens: HeadlessTokenQuantity(e.PromptCacheWriteTokens),
		RetryCount:             HeadlessObservedCount(e.RetryCount), FallbackCount: HeadlessObservedCount(e.FallbackCount),
		AffinitySupport: e.AffinitySupport.String(),
		ReportedCost: HeadlessReportedCost{
			Amount: e.ReportedCost.Amount, Currency: string(e.ReportedCost.Currency),
			Reported: e.ReportedCost.Reported, CoverageKnown: e.ReportedCost.CoverageKnown, Source: e.ReportedCost.Source,
		},
	}
}

func projectPricingProvenance(p llm.MetadataProvenance) HeadlessPricingProvenance {
	return HeadlessPricingProvenance{Source: p.Source.String(), Snapshot: p.Snapshot,
		Revision: p.Revision, ObservedAt: p.ObservedAt, ExpiresAt: p.ExpiresAt}
}

func projectRateCard(card llm.RateCard) *HeadlessRateCard {
	out := &HeadlessRateCard{
		Reference: HeadlessRateCardReference(card.Reference), PricingProvider: string(card.PricingProvider),
		Currency: string(card.Currency), Billing: card.Billing.String(), Provenance: projectPricingProvenance(card.Provenance),
		Applicable: card.Applicable, TokenOnly: card.TokenOnly,
		FreshInput: HeadlessTokenRate(card.FreshInput), CacheRead: HeadlessTokenRate(card.CacheRead), Output: HeadlessTokenRate(card.Output),
	}
	if card.CacheWrites != nil {
		out.CacheWrites = make([]HeadlessCacheWriteRate, len(card.CacheWrites))
		for i, row := range card.CacheWrites {
			out.CacheWrites[i] = HeadlessCacheWriteRate{LifetimeNS: int64(row.Lifetime), Rate: HeadlessTokenRate(row.Rate)}
		}
	}
	return out
}

func projectPriceEstimate(estimate llm.PriceEstimate) *HeadlessPriceEstimate {
	out := &HeadlessPriceEstimate{
		Reference: HeadlessRateCardReference(estimate.Reference), PricingProvider: string(estimate.PricingProvider),
		Currency: string(estimate.Currency), Billing: estimate.Billing.String(), Provenance: projectPricingProvenance(estimate.Provenance),
		InputAmount: estimate.InputAmount, OutputAmount: estimate.OutputAmount, Subtotal: estimate.Subtotal,
		HasKnownSubtotal: estimate.HasKnownSubtotal, Complete: estimate.Complete,
	}
	if estimate.Issues != nil {
		out.Issues = make([]HeadlessPricingIssue, len(estimate.Issues))
		for i, issue := range estimate.Issues {
			out.Issues[i] = HeadlessPricingIssue{Kind: issue.Kind.String(), Field: issue.Field, LifetimeNS: int64(issue.Lifetime)}
		}
	}
	return out
}
