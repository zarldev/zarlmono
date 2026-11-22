package backends_test

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestEstimatePricePresenceAndCoverage(t *testing.T) {
	issues := llm.PricingIssueKinds
	for _, tc := range []struct {
		name     string
		change   func(*llm.RateCard, *llm.PricingUsage)
		input    float64
		output   float64
		known    bool
		complete bool
		issues   []llm.PricingIssue
	}{
		{name: "mixed fresh read and two write lifetimes", input: 20, output: 20, known: true, complete: true},
		{name: "partial fresh rate", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.FreshInput.Known = false }, input: 15, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNRATE, Field: "input.fresh"}}},
		{name: "partial output rate", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.Output.Known = false }, input: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNRATE, Field: "output.text"}}},
		{name: "partial read rate", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.CacheRead.Known = false }, input: 16, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNRATE, Field: "input.cache_read"}}},
		{name: "unknown read count is not zero", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.CacheRead = llm.TokenQuantity{} }, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "input.cache_read"}}},
		{name: "unknown write count is not zero", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.CacheWrite = llm.TokenQuantity{} }, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "input.cache_write"}}},
		{name: "unknown input other prevents residual", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.Other.Known = false }, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "input.other"}}},
		{name: "unknown input total", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.Total.Known = false }, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "input.total"}}},
		{name: "unresolved overlap", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.PartitionKnown = false }, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNSUPPORTEDDIMENSIONS, Field: "input.partition"}}},
		{name: "unsupported disjoint input is not text", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.Other = pricingTokens(1_000_000) }, input: 19, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNSUPPORTEDDIMENSIONS, Field: "input.other"}}},
		{name: "unsupported disjoint output is not text", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Output.Other = pricingTokens(1_000_000) }, input: 20, output: 15, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNSUPPORTEDDIMENSIONS, Field: "output.other"}}},
		{name: "unknown output other prevents residual", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Output.Other.Known = false }, input: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "output.other"}}},
		{name: "unknown output total", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Output.Total.Known = false }, input: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "output.total"}}},
		{name: "additional charges", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.AdditionalChargesPossible = true }, input: 20, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNSUPPORTEDDIMENSIONS, Field: "additional_charges"}}},
		{name: "card not token only", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.TokenOnly = false }, input: 20, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNSUPPORTEDDIMENSIONS, Field: "token_only"}}},
		{name: "unknown lifetime no fresh fallback", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.WritesByLifetime[0].Lifetime = 0 }, input: 17, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNWRITELIFETIME, Field: "input.writes_by_lifetime"}}},
		{name: "uncovered lifetime residual", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.WritesByLifetime = u.Input.WritesByLifetime[:1] }, input: 12, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNWRITELIFETIME, Field: "input.cache_write"}}},
		{name: "no lifetime rows do not charge aggregate", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.WritesByLifetime = nil }, input: 9, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNWRITELIFETIME, Field: "input.cache_write"}}},
		{name: "missing exact lifetime rate", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.CacheWrites = c.CacheWrites[:1] }, input: 12, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNRATE, Field: "input.writes_by_lifetime", Lifetime: time.Hour}}},
		{name: "unknown lifetime rate", change: func(c *llm.RateCard, _ *llm.PricingUsage) { c.CacheWrites[1].Rate.Known = false }, input: 12, output: 20, known: true,
			issues: []llm.PricingIssue{{Kind: issues.PRICINGUNKNOWNRATE, Field: "input.writes_by_lifetime", Lifetime: time.Hour}}},
		{name: "unknown lifetime quantity", change: func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.WritesByLifetime[1].Quantity.Known = false }, input: 12, output: 20, known: true,
			issues: []llm.PricingIssue{
				{Kind: issues.PRICINGUNKNOWNQUANTITY, Field: "input.writes_by_lifetime", Lifetime: time.Hour},
				{Kind: issues.PRICINGUNKNOWNWRITELIFETIME, Field: "input.cache_write"},
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card, usage := pricingFixture()
			if tc.change != nil {
				tc.change(&card, &usage)
			}
			got := backends.EstimatePrice(card, usage)
			if got.InputAmount != tc.input || got.OutputAmount != tc.output || got.Subtotal != tc.input+tc.output ||
				got.HasKnownSubtotal != tc.known || got.Complete != tc.complete || !slices.Equal(got.Issues, tc.issues) {
				t.Fatalf("estimate = %+v; want input=%v output=%v known=%v complete=%v issues=%v", got, tc.input, tc.output, tc.known, tc.complete, tc.issues)
			}
		})
	}
}

func TestEstimatePriceExplicitZerosAndAbsentUsage(t *testing.T) {
	card, _ := pricingFixture()
	card.FreshInput, card.CacheRead, card.Output, card.CacheWrites = llm.TokenRate{}, llm.TokenRate{}, llm.TokenRate{}, nil
	zeroUsage := llm.PricingUsage{
		Input:  llm.PricingInputUsage{Total: pricingTokens(0), CacheRead: pricingTokens(0), CacheWrite: pricingTokens(0), Other: pricingTokens(0), PartitionKnown: true},
		Output: llm.PricingOutputUsage{Total: pricingTokens(0), Other: pricingTokens(0)},
	}
	got := backends.EstimatePrice(card, zeroUsage)
	if !got.HasKnownSubtotal || !got.Complete || got.Subtotal != 0 || len(got.Issues) != 0 {
		t.Fatalf("explicit zero quantities should not need rates: %+v", got)
	}
	// A zero lifetime on zero writes omits no charge.
	zeroUsage.Input.WritesByLifetime = []llm.CacheWriteQuantity{{Quantity: pricingTokens(0)}}
	if got := backends.EstimatePrice(card, zeroUsage); !got.Complete || !got.HasKnownSubtotal {
		t.Fatalf("zero lifetime zero writes = %+v", got)
	}
	got = backends.EstimatePrice(card, llm.PricingUsage{})
	if got.HasKnownSubtotal || got.Complete || got.Subtotal != 0 {
		t.Fatalf("absent quantities must not establish a known zero: %+v", got)
	}
	card, usage := pricingFixture()
	card.FreshInput, card.CacheRead, card.Output = pricingRate(0), pricingRate(0), pricingRate(0)
	for i := range card.CacheWrites {
		card.CacheWrites[i].Rate = pricingRate(0)
	}
	got = backends.EstimatePrice(card, usage)
	if !got.HasKnownSubtotal || !got.Complete || got.Subtotal != 0 {
		t.Fatalf("explicit free rates = %+v", got)
	}
	card.FreshInput.Known, card.CacheRead.Known, card.Output.Known = false, false, false
	for i := range card.CacheWrites {
		card.CacheWrites[i].Rate.Known = false
	}
	got = backends.EstimatePrice(card, usage)
	if got.HasKnownSubtotal || got.Complete || got.Subtotal != 0 {
		t.Fatalf("absent rates with positive quantities = %+v", got)
	}
	// Inclusive output already includes reasoning: only Total is priced once.
	card, usage = pricingFixture()
	usage.Output.Total = pricingTokens(7_000_000)
	if got := backends.EstimatePrice(card, usage); got.OutputAmount != 35 || !got.Complete {
		t.Fatalf("inclusive output = %+v", got)
	}
}

func TestEstimatePriceRejectsUnresolvedBasis(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*llm.RateCard)
		issue  llm.PricingIssueKind
	}{
		{"unknown billing", func(c *llm.RateCard) { c.Billing = llm.BillingModes.BILLINGUNKNOWN }, llm.PricingIssueKinds.PRICINGUNKNOWNBILLING},
		{"subscription", func(c *llm.RateCard) { c.Billing = llm.BillingModes.BILLINGSUBSCRIPTION }, llm.PricingIssueKinds.PRICINGUNKNOWNBILLING},
		{"unmetered", func(c *llm.RateCard) { c.Billing = llm.BillingModes.BILLINGUNMETERED }, llm.PricingIssueKinds.PRICINGUNKNOWNBILLING},
		{"unresolved applicability", func(c *llm.RateCard) { c.Applicable = false }, llm.PricingIssueKinds.PRICINGUNRESOLVEDAPPLICABILITY},
		{"missing id", func(c *llm.RateCard) { c.Reference.ID = "" }, llm.PricingIssueKinds.PRICINGUNKNOWNBASIS},
		{"missing revision", func(c *llm.RateCard) { c.Reference.Revision = "" }, llm.PricingIssueKinds.PRICINGUNKNOWNBASIS},
		{"missing entry", func(c *llm.RateCard) { c.Reference.Entry = "" }, llm.PricingIssueKinds.PRICINGUNKNOWNBASIS},
		{"missing currency", func(c *llm.RateCard) { c.Currency = "" }, llm.PricingIssueKinds.PRICINGUNKNOWNBASIS},
		{"missing pricing provider", func(c *llm.RateCard) { c.PricingProvider = "" }, llm.PricingIssueKinds.PRICINGUNKNOWNBASIS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card, usage := pricingFixture()
			tc.change(&card)
			got := backends.EstimatePrice(card, usage)
			if got.HasKnownSubtotal || got.Complete || got.InputAmount != 0 || got.OutputAmount != 0 || got.Subtotal != 0 ||
				len(got.Issues) != 1 || got.Issues[0].Kind != tc.issue || got.Billing != card.Billing {
				t.Fatalf("unresolved card produced pricing: %+v", got)
			}
		})
	}
	got := backends.EstimatePrice(llm.RateCard{}, llm.PricingUsage{})
	if got.HasKnownSubtotal || got.Complete || got.Subtotal != 0 || len(got.Issues) != 3 {
		t.Fatalf("zero-value unknown = %+v", got)
	}
}

func TestEstimatePriceContradictionsOmitDirection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*llm.RateCard, *llm.PricingUsage)
		output bool
	}{
		{"input subsets exceed total", func(_ *llm.RateCard, u *llm.PricingUsage) { u.Input.Total = pricingTokens(4_000_000) }, false},
		{"write subsets exceed aggregate", func(_ *llm.RateCard, u *llm.PricingUsage) {
			u.Input.WritesByLifetime[1].Quantity = pricingTokens(3_000_000)
		}, false},
		{"duplicate quantity lifetime", func(_ *llm.RateCard, u *llm.PricingUsage) {
			u.Input.WritesByLifetime[1].Lifetime = u.Input.WritesByLifetime[0].Lifetime
		}, false},
		{"duplicate rate lifetime", func(c *llm.RateCard, _ *llm.PricingUsage) { c.CacheWrites[1].Lifetime = c.CacheWrites[0].Lifetime }, false},
		{"output subset exceeds total", func(_ *llm.RateCard, u *llm.PricingUsage) { u.Output.Other = pricingTokens(5_000_000) }, true},
		{"input subset sum would overflow", func(_ *llm.RateCard, u *llm.PricingUsage) {
			u.Input.Total, u.Input.CacheRead, u.Input.CacheWrite = pricingTokens(math.MaxInt64), pricingTokens(math.MaxInt64), pricingTokens(math.MaxInt64)
		}, false},
		{"write subset sum would overflow", func(_ *llm.RateCard, u *llm.PricingUsage) {
			u.Input.Total, u.Input.CacheWrite, u.Input.CacheRead = pricingTokens(math.MaxInt64), pricingTokens(math.MaxInt64), pricingTokens(0)
			for i := range u.Input.WritesByLifetime {
				u.Input.WritesByLifetime[i].Quantity = pricingTokens(math.MaxInt64)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card, usage := pricingFixture()
			tc.change(&card, &usage)
			got := backends.EstimatePrice(card, usage)
			wantInput, wantOutput := 0.0, 20.0
			if tc.output {
				wantInput, wantOutput = 20, 0
			}
			if got.InputAmount != wantInput || got.OutputAmount != wantOutput || got.Subtotal != 20 || !got.HasKnownSubtotal || got.Complete ||
				len(got.Issues) != 1 || got.Issues[0].Kind != llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS {
				t.Fatalf("contradiction was clamped, charged or lost independent direction: %+v", got)
			}
		})
	}
}

func TestEstimatePriceArithmeticOverflow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*llm.RateCard, *llm.PricingUsage)
		input  float64
		output float64
		known  bool
		field  string
	}{
		{"fresh contribution", func(c *llm.RateCard, _ *llm.PricingUsage) { c.FreshInput = pricingRate(math.MaxFloat64) }, 0, 20, true, "input.fresh"},
		{"output contribution", func(c *llm.RateCard, _ *llm.PricingUsage) { c.Output = pricingRate(math.MaxFloat64) }, 20, 0, true, "output.text"},
		{"input direction sum", func(c *llm.RateCard, u *llm.PricingUsage) {
			u.Input.Total, u.Input.CacheRead, u.Input.CacheWrite = pricingTokens(2_000_000), pricingTokens(1_000_000), pricingTokens(0)
			u.Input.WritesByLifetime = nil
			c.FreshInput, c.CacheRead = pricingRate(math.MaxFloat64*0.75), pricingRate(math.MaxFloat64*0.75)
		}, 0, 20, true, "input.cache_read"},
		{"subtotal sum", func(c *llm.RateCard, u *llm.PricingUsage) {
			u.Input.Total, u.Input.CacheRead, u.Input.CacheWrite = pricingTokens(1_000_000), pricingTokens(0), pricingTokens(0)
			u.Input.WritesByLifetime = nil
			u.Output.Total = pricingTokens(1_000_000)
			c.FreshInput, c.Output = pricingRate(math.MaxFloat64*0.75), pricingRate(math.MaxFloat64*0.75)
		}, math.MaxFloat64 * 0.75, math.MaxFloat64 * 0.75, false, "subtotal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card, usage := pricingFixture()
			tc.change(&card, &usage)
			got := backends.EstimatePrice(card, usage)
			wantSubtotal := tc.input + tc.output
			if !tc.known {
				wantSubtotal = 0
			}
			if got.InputAmount != tc.input || got.OutputAmount != tc.output || got.Subtotal != wantSubtotal || got.HasKnownSubtotal != tc.known || got.Complete ||
				!slices.Equal(got.Issues, []llm.PricingIssue{{Kind: llm.PricingIssueKinds.PRICINGARITHMETICOVERFLOW, Field: tc.field}}) {
				t.Fatalf("overflow estimate = %+v", got)
			}
		})
	}
	// Divide before multiplying: a finite mathematical charge must not overflow
	// merely because the unscaled product would overflow.
	card, usage := pricingFixture()
	usage.Output.Total = pricingTokens(1)
	card.Output = pricingRate(math.MaxFloat64)
	got := backends.EstimatePrice(card, usage)
	if !got.Complete || !got.HasKnownSubtotal || math.IsInf(got.OutputAmount, 0) || got.OutputAmount <= 0 {
		t.Fatalf("finite per-million calculation = %+v", got)
	}
}

func TestEstimatePriceSnapshotIdentityAndNoMutation(t *testing.T) {
	card, usage := pricingFixture()
	beforeCard, beforeUsage := card.Clone(), usage.Clone()
	got := backends.EstimatePrice(card, usage)
	if got.Reference != card.Reference || got.Currency != card.Currency || got.PricingProvider != card.PricingProvider || got.Billing != card.Billing || got.Provenance != card.Provenance {
		t.Fatalf("pricing basis changed: %+v", got)
	}
	if !reflect.DeepEqual(card, beforeCard) || !reflect.DeepEqual(usage, beforeUsage) {
		t.Fatal("estimation mutated caller snapshots")
	}
	// Estimates retain no mutable aliases of card/usage collections, and each
	// call independently owns any diagnostics it returns.
	usage.Input.PartitionKnown = false
	first, second := backends.EstimatePrice(card, usage), backends.EstimatePrice(card, usage)
	first.Issues[0].Field = "changed"
	if second.Issues[0].Field != "input.partition" {
		t.Fatal("calls shared diagnostics")
	}
	card.Reference.Entry = "changed"
	if got.Reference.Entry != beforeCard.Reference.Entry {
		t.Fatal("estimate identity was not a value snapshot")
	}
}

func pricingTokens(tokens int64) llm.TokenQuantity {
	return llm.TokenQuantity{Tokens: tokens, Known: true}
}

func pricingRate(rate float64) llm.TokenRate {
	return llm.TokenRate{PerMillionTokens: rate, Known: true}
}

func pricingFixture() (llm.RateCard, llm.PricingUsage) {
	observed := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	return llm.RateCard{
		Reference:       llm.RateCardReference{ID: "selected-flat-card", Revision: "rev-2", Entry: "deployment-tier"},
		PricingProvider: "rate-owner", Currency: "opaque-currency", Billing: llm.BillingModes.BILLINGTOKENMETERED,
		Provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION, Snapshot: "selected-snapshot", Revision: "metadata-3", ObservedAt: observed, ExpiresAt: observed.Add(time.Hour)},
		Applicable: true, TokenOnly: true, FreshInput: pricingRate(1), CacheRead: pricingRate(2), Output: pricingRate(5),
		CacheWrites: []llm.CacheWriteRate{{Lifetime: 5 * time.Minute, Rate: pricingRate(3)}, {Lifetime: time.Hour, Rate: pricingRate(4)}},
	}, llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total: pricingTokens(10_000_000), CacheRead: pricingTokens(2_000_000), CacheWrite: pricingTokens(3_000_000), Other: pricingTokens(0), PartitionKnown: true,
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: pricingTokens(1_000_000)}, {Lifetime: time.Hour, Quantity: pricingTokens(2_000_000)}},
		},
		Output: llm.PricingOutputUsage{Total: pricingTokens(4_000_000), Other: pricingTokens(0)},
	}
}
