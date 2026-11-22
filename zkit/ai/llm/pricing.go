package llm

import (
	"slices"
	"time"
)

// TokenQuantity distinguishes absent counts from explicitly reported zero tokens.
// Known counts are nonnegative; external boundaries own scalar validation.
type TokenQuantity struct {
	Tokens int64
	Known  bool
}

// TokenRate is a selected flat rate per million tokens in the card's currency.
// Known rates are finite and nonnegative; a known zero rate is free, not absent.
type TokenRate struct {
	PerMillionTokens float64
	Known            bool
}

// CacheWriteQuantity describes a subset of aggregate cache writes with one
// lifetime. Zero lifetime means unknown, not a default lifetime.
type CacheWriteQuantity struct {
	Lifetime time.Duration
	Quantity TokenQuantity
}

// CacheWriteRate prices writes with exactly this lifetime, without fallback.
type CacheWriteRate struct {
	Lifetime time.Duration
	Rate     TokenRate
}

// PricingInputUsage describes inclusive input tokens. PartitionKnown asserts
// that CacheRead, CacheWrite and unsupported Other are disjoint subsets of Total.
// WritesByLifetime partitions CacheWrite, never an additional billable count.
// Value copies borrow the slice; Clone makes an independently owned snapshot.
type PricingInputUsage struct {
	Total, CacheRead, CacheWrite, Other TokenQuantity
	WritesByLifetime                    []CacheWriteQuantity
	PartitionKnown                      bool
}

// PricingOutputUsage describes inclusive output (including reasoning counted
// once) and its disjoint unsupported Other subset. Unknown Other prevents text
// residual derivation; unsupported categories must not receive the text rate.
type PricingOutputUsage struct {
	Total, Other TokenQuantity
}

// PricingUsage is an explicit presence-aware sidecar, not an interpretation of
// legacy Usage. AdditionalChargesPossible prevents complete token-only coverage.
type PricingUsage struct {
	Input                     PricingInputUsage
	Output                    PricingOutputUsage
	AdditionalChargesPossible bool
}

// Clone returns usage with independently owned lifetime quantities.
func (u PricingUsage) Clone() PricingUsage {
	u.Input.WritesByLifetime = slices.Clone(u.Input.WritesByLifetime)
	return u
}

// Currency is an open currency identifier. No conversion is implied.
type Currency string

// PricingProviderID identifies the organization whose selected rates apply,
// independently of routing or serving-provider identity.
type PricingProviderID string

// RateCard is a selected flat-rate snapshot for an exact reference entry and
// revision. Applicable asserts that tier, region, account and other selection
// conditions have been resolved. TokenOnly asserts there are no other billing
// dimensions. Missing identity or rates remain unknown, not inferred defaults.
// Value copies borrow CacheWrites; Clone makes an independently owned snapshot.
type RateCard struct {
	Reference                     RateCardReference
	PricingProvider               PricingProviderID
	Currency                      Currency
	Billing                       BillingMode
	Provenance                    MetadataProvenance
	Applicable                    bool
	TokenOnly                     bool
	FreshInput, CacheRead, Output TokenRate
	CacheWrites                   []CacheWriteRate
}

// Clone returns a card with independently owned cache-write rates.
func (c RateCard) Clone() RateCard {
	c.CacheWrites = slices.Clone(c.CacheWrites)
	return c
}

// PricingIssue identifies omitted coverage or inconsistent arithmetic inputs.
// Field is a diagnostic path, not a dispatch vocabulary. Lifetime identifies
// an affected cache-write lifetime when relevant; zero means unknown.
type PricingIssue struct {
	Kind     PricingIssueKind
	Field    string
	Lifetime time.Duration
}

// PriceEstimate preserves its selected pricing basis and sums only known token
// contributions. Subtotal is neither an invoice nor a guaranteed lower bound.
// HasKnownSubtotal distinguishes a known zero from no numeric evidence. Complete
// requires all quantities and charges covered, not merely a numeric subtotal.
// Value copies borrow Issues; Clone makes an independently owned snapshot.
type PriceEstimate struct {
	Reference                           RateCardReference
	PricingProvider                     PricingProviderID
	Currency                            Currency
	Billing                             BillingMode
	Provenance                          MetadataProvenance
	InputAmount, OutputAmount, Subtotal float64
	HasKnownSubtotal                    bool
	Complete                            bool
	Issues                              []PricingIssue
}

// Clone returns an estimate with independently owned coverage diagnostics.
func (e PriceEstimate) Clone() PriceEstimate {
	e.Issues = slices.Clone(e.Issues)
	return e
}

// ReportedCost retains provider-reported cost separately from estimates. Reported
// distinguishes absent cost from reported zero; CoverageKnown is independent.
// Source is a bounded nonsecret opaque ID validated at a future wire boundary.
// Reported cost must not be added to an estimate of the same usage.
type ReportedCost struct {
	Amount        float64
	Currency      Currency
	Reported      bool
	CoverageKnown bool
	Source        string
}
