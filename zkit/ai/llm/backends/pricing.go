package backends

import (
	"math"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// EstimatePrice calculates only supported, disjoint token quantities against an
// already-selected flat rate card. It performs no discovery, I/O or clock reads,
// does not mutate its inputs, and never incorporates provider-reported cost.
// Scalars must already be nonnegative and finite at their external boundary.
//
// Unknown partitioning omits input rather than charging overlapping categories.
// Inconsistent subsets, duplicate lifetime rows or nonfinite arithmetic omit the
// entire affected direction, preserving an independently valid other direction.
// If only the final sum overflows, direction amounts remain available but
// Subtotal is zero and HasKnownSubtotal is false. Unknown rates or lifetimes
// otherwise omit only their own contributions. Known zero quantities require no
// rate. Complete means every applicable charge is covered, not just text tokens.
func EstimatePrice(card llm.RateCard, usage llm.PricingUsage) llm.PriceEstimate {
	estimate := llm.PriceEstimate{
		Reference: card.Reference, PricingProvider: card.PricingProvider,
		Currency: card.Currency, Billing: card.Billing, Provenance: card.Provenance,
	}
	if card.Billing != llm.BillingModes.BILLINGTOKENMETERED {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGUNKNOWNBILLING, "billing", 0))
	}
	if card.Reference.ID == "" || card.Reference.Revision == "" || card.Reference.Entry == "" ||
		card.Currency == "" || card.PricingProvider == "" {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGUNKNOWNBASIS, "basis", 0))
	}
	if !card.Applicable {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGUNRESOLVEDAPPLICABILITY, "applicable", 0))
	}
	if len(estimate.Issues) != 0 {
		return estimate
	}
	if !card.TokenOnly {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, "token_only", 0))
	}
	if usage.AdditionalChargesPossible {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, "additional_charges", 0))
	}
	input := priceInput(card, usage.Input)
	output := priceOutput(card.Output, usage.Output)
	estimate.InputAmount, estimate.OutputAmount = input.amount, output.amount
	estimate.Issues = append(estimate.Issues, input.issues...)
	estimate.Issues = append(estimate.Issues, output.issues...)
	subtotal := input.amount + output.amount
	if math.IsInf(subtotal, 0) || math.IsNaN(subtotal) {
		estimate.Issues = append(estimate.Issues, pricingIssue(llm.PricingIssueKinds.PRICINGARITHMETICOVERFLOW, "subtotal", 0))
		return estimate
	}
	estimate.Subtotal = subtotal
	estimate.HasKnownSubtotal = input.known || output.known
	estimate.Complete = len(estimate.Issues) == 0
	return estimate
}

type pricingDirection struct {
	amount   float64
	known    bool
	overflow bool
	issues   []llm.PricingIssue
}

func (d *pricingDirection) issue(kind llm.PricingIssueKind, field string, lifetime time.Duration) {
	d.issues = append(d.issues, pricingIssue(kind, field, lifetime))
}

func (d *pricingDirection) charge(tokens int64, rate llm.TokenRate, field string, lifetime time.Duration) {
	if tokens != 0 && !rate.Known {
		d.issue(llm.PricingIssueKinds.PRICINGUNKNOWNRATE, field, lifetime)
		return
	}
	amount := float64(tokens) / 1_000_000 * rate.PerMillionTokens
	// Unknown rates have no numeric meaning, including their stored scalar.
	if tokens == 0 {
		amount = 0
	}
	if math.IsInf(amount, 0) || math.IsNaN(amount) || math.IsInf(d.amount+amount, 0) {
		d.issue(llm.PricingIssueKinds.PRICINGARITHMETICOVERFLOW, field, lifetime)
		d.overflow = true
		return
	}
	d.amount += amount
	d.known = true
}

func (d pricingDirection) result() pricingDirection {
	if d.overflow {
		d.amount, d.known = 0, false
	}
	return d
}

func pricingIssue(kind llm.PricingIssueKind, field string, lifetime time.Duration) llm.PricingIssue {
	return llm.PricingIssue{Kind: kind, Field: field, Lifetime: lifetime}
}

func priceInput(card llm.RateCard, usage llm.PricingInputUsage) pricingDirection {
	var result pricingDirection
	if !usage.PartitionKnown {
		result.issue(llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, "input.partition", 0)
		return result
	}
	quantities := []struct {
		field string
		value llm.TokenQuantity
	}{
		{"input.total", usage.Total}, {"input.cache_read", usage.CacheRead},
		{"input.cache_write", usage.CacheWrite}, {"input.other", usage.Other},
	}
	for _, quantity := range quantities {
		if !quantity.value.Known {
			result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNQUANTITY, quantity.field, 0)
		}
	}
	if len(result.issues) != 0 {
		return result
	}
	fresh := usage.Total.Tokens
	for _, subset := range []int64{usage.CacheRead.Tokens, usage.CacheWrite.Tokens, usage.Other.Tokens} {
		if subset > fresh {
			result.issue(llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS, "input", 0)
			return result
		}
		fresh -= subset
	}
	if !checkWriteSubsets(&result, usage.CacheWrite.Tokens, usage.WritesByLifetime, card.CacheWrites) {
		return result
	}
	if usage.Other.Tokens != 0 {
		result.issue(llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, "input.other", 0)
	}
	result.charge(fresh, card.FreshInput, "input.fresh", 0)
	result.charge(usage.CacheRead.Tokens, card.CacheRead, "input.cache_read", 0)
	priceWrites(&result, usage.CacheWrite.Tokens, usage.WritesByLifetime, card.CacheWrites)
	return result.result()
}

// checkWriteSubsets checks known counts with bounded subtraction. Unknown row
// quantities still permit pricing independently known rows, never a residual.
func checkWriteSubsets(result *pricingDirection, total int64, quantities []llm.CacheWriteQuantity, rates []llm.CacheWriteRate) bool {
	seen := make(map[time.Duration]bool, len(quantities))
	remaining := total
	for _, row := range quantities {
		if seen[row.Lifetime] || (row.Quantity.Known && row.Quantity.Tokens > remaining) {
			result.issue(llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS, "input.writes_by_lifetime", row.Lifetime)
			return false
		}
		seen[row.Lifetime] = true
		if row.Quantity.Known {
			remaining -= row.Quantity.Tokens
		}
	}
	seen = make(map[time.Duration]bool, len(rates))
	for _, row := range rates {
		if seen[row.Lifetime] {
			result.issue(llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS, "cache_writes", row.Lifetime)
			return false
		}
		seen[row.Lifetime] = true
	}
	return true
}

func priceWrites(result *pricingDirection, total int64, quantities []llm.CacheWriteQuantity, rates []llm.CacheWriteRate) {
	selected := make(map[time.Duration]llm.TokenRate, len(rates))
	for _, row := range rates {
		selected[row.Lifetime] = row.Rate
	}
	remaining := total
	for _, row := range quantities {
		if !row.Quantity.Known {
			result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNQUANTITY, "input.writes_by_lifetime", row.Lifetime)
			continue
		}
		remaining -= row.Quantity.Tokens
		if row.Lifetime == 0 && row.Quantity.Tokens != 0 {
			result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNWRITELIFETIME, "input.writes_by_lifetime", 0)
			continue
		}
		result.charge(row.Quantity.Tokens, selected[row.Lifetime], "input.writes_by_lifetime", row.Lifetime)
	}
	if remaining != 0 {
		result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNWRITELIFETIME, "input.cache_write", 0)
	} else if total == 0 {
		result.charge(0, llm.TokenRate{}, "input.cache_write", 0)
	}
}

func priceOutput(rate llm.TokenRate, usage llm.PricingOutputUsage) pricingDirection {
	var result pricingDirection
	if !usage.Total.Known {
		result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNQUANTITY, "output.total", 0)
	}
	if !usage.Other.Known {
		result.issue(llm.PricingIssueKinds.PRICINGUNKNOWNQUANTITY, "output.other", 0)
	}
	if len(result.issues) != 0 {
		return result
	}
	if usage.Other.Tokens > usage.Total.Tokens {
		result.issue(llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS, "output", 0)
		return result
	}
	if usage.Other.Tokens != 0 {
		result.issue(llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, "output.other", 0)
	}
	result.charge(usage.Total.Tokens-usage.Other.Tokens, rate, "output.text", 0)
	return result.result()
}
