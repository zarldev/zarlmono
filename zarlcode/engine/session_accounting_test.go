package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func recordedAccountingFixture() engine.RecordedProviderAccounting {
	q := func(n int64) llm.TokenQuantity { return llm.TokenQuantity{Tokens: n, Known: true} }
	rate := func(n float64) llm.TokenRate { return llm.TokenRate{PerMillionTokens: n, Known: true} }
	card := llm.RateCard{
		Reference:       llm.RateCardReference{ID: " Policy / A ", Revision: "r1", Entry: "Mixed/Case"},
		PricingProvider: " Operator ", Currency: "EUR", Billing: llm.BillingModes.BILLINGTOKENMETERED,
		Provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION,
			Snapshot: "snapshot", Revision: "digest", ObservedAt: time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC),
			ExpiresAt: time.Date(2027, 1, 2, 3, 4, 5, 6, time.UTC)},
		Applicable: true, TokenOnly: false, FreshInput: rate(2), CacheRead: rate(0), Output: rate(3),
		CacheWrites: []llm.CacheWriteRate{{Lifetime: 5 * time.Minute, Rate: rate(4)}, {Lifetime: time.Hour, Rate: rate(6)}, {Lifetime: 0}},
	}
	usage := llm.PricingUsage{
		Input: llm.PricingInputUsage{Total: q(1_000_000), CacheRead: q(200_000), CacheWrite: q(400_000), Other: q(0), PartitionKnown: true,
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: q(100_000)}, {Lifetime: time.Hour, Quantity: q(300_000)}, {Lifetime: 0}}},
		Output: llm.PricingOutputUsage{Total: q(500_000), Other: q(0)}, AdditionalChargesPossible: true,
	}
	estimate := backends.EstimatePrice(card, usage)
	return engine.RecordedProviderAccounting{
		RequestBatchID: " Receipt / β ", TaskID: " Task / A ", Attempt: 2, Provider: " Gateway / A ", Model: " Team/Mixed Alias ",
		Usage: &llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000,
			CachedTokens: 200_000, CacheCreationTokens: 400_000, CacheCreation5mTokens: 100_000, CacheCreation1hTokens: 300_000,
			ReasoningTokens: 0, ReasoningTokensReported: true},
		PricingUsage: &usage, RateCard: &card, ConfiguredEstimate: &estimate,
		ReportedCost: &llm.ReportedCost{Amount: 17, Currency: "JPY", Reported: true, CoverageKnown: false},
	}
}

func encodeRecordedAccounting(t *testing.T, r engine.RecordedProviderAccounting) []byte {
	t.Helper()
	data, err := engine.EncodeRecordedProviderAccounting(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeRecordedAccounting(t *testing.T, data []byte) engine.RecordedProviderAccounting {
	t.Helper()
	r, err := engine.DecodeRecordedProviderAccounting(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordedProviderAccountingRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedProviderAccounting)
	}{
		{name: "partial non USD mixed cache lifetime coverage"},
		{name: "unknown currency and billing", edit: func(r *engine.RecordedProviderAccounting) {
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = "", "", ""
			r.RateCard.Billing, r.ConfiguredEstimate.Billing = llm.BillingMode{}, llm.BillingMode{}
		}},
		{name: "unknown partition and independent output", edit: func(r *engine.RecordedProviderAccounting) {
			r.PricingUsage.Input.PartitionKnown = false
			r.PricingUsage.Input.CacheRead = llm.TokenQuantity{}
		}},
		{name: "open currencies and labels", edit: func(r *engine.RecordedProviderAccounting) {
			r.Provider, r.Model = "", ""
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = " X-Credit ", " X-Credit ", " X-Credit "
		}},
		{name: "integer and duration limits stay exact", edit: func(r *engine.RecordedProviderAccounting) {
			r.Attempt, r.Usage.TotalTokens = math.MaxInt, math.MaxInt
			r.PricingUsage.Input.Total.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Lifetime = time.Duration(math.MaxInt64)
			r.RateCard.CacheWrites[0].Lifetime = time.Duration(math.MaxInt64)
			r.ConfiguredEstimate.Issues[0].Lifetime = time.Duration(math.MaxInt64)
		}},
		{name: "nil slices", edit: func(r *engine.RecordedProviderAccounting) {
			r.PricingUsage.Input.WritesByLifetime, r.RateCard.CacheWrites, r.ConfiguredEstimate.Issues = nil, nil, nil
		}},
		{name: "present empty slices", edit: func(r *engine.RecordedProviderAccounting) {
			r.PricingUsage.Input.WritesByLifetime = []llm.CacheWriteQuantity{}
			r.RateCard.CacheWrites = []llm.CacheWriteRate{}
			r.ConfiguredEstimate.Issues = []llm.PricingIssue{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := recordedAccountingFixture()
			if tc.edit != nil {
				tc.edit(&want)
			}
			got := decodeRecordedAccounting(t, encodeRecordedAccounting(t, want))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed evidence: got %+v; want %+v", got, want)
			}
		})
	}
}

func TestRecordedProviderAccountingPresenceAndAvailability(t *testing.T) {
	t.Parallel()
	// Exercise every optional-pointer combination, including frozen estimates
	// whose supporting snapshots are absent. Availability is not recomputation.
	for mask := range 32 {
		r := engine.RecordedProviderAccounting{RequestBatchID: "r", TaskID: "t", Attempt: 1}
		if mask&1 != 0 {
			r.Usage = &llm.Usage{}
		}
		if mask&2 != 0 {
			r.PricingUsage = &llm.PricingUsage{}
		}
		if mask&4 != 0 {
			r.RateCard = &llm.RateCard{}
		}
		if mask&8 != 0 {
			r.ConfiguredEstimate = &llm.PriceEstimate{}
		}
		if mask&16 != 0 {
			r.ReportedCost = &llm.ReportedCost{}
		}
		data := encodeRecordedAccounting(t, r)
		got := decodeRecordedAccounting(t, data)
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("pointer presence changed for mask %d", mask)
		}
		availability := "not_computed"
		switch {
		case r.ConfiguredEstimate != nil:
			availability = "available"
		case r.RateCard == nil && r.PricingUsage == nil:
			availability = "both_missing"
		case r.RateCard == nil:
			availability = "missing_card"
		case r.PricingUsage == nil:
			availability = "missing_usage"
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["estimate_availability"]) != `"`+availability+`"` {
			t.Fatalf("availability for mask %d = %s", mask, fields["estimate_availability"])
		}
	}
	zero := recordedAccountingFixture()
	zero.PricingUsage = &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}
	zero.RateCard = &llm.RateCard{FreshInput: llm.TokenRate{Known: true}}
	zero.ConfiguredEstimate = &llm.PriceEstimate{HasKnownSubtotal: true, Complete: true}
	zero.ReportedCost = &llm.ReportedCost{Reported: true, CoverageKnown: true}
	if got := decodeRecordedAccounting(t, encodeRecordedAccounting(t, zero)); !reflect.DeepEqual(got, zero) {
		t.Fatal("known zero became absent or unknown")
	}
}

func TestRecordedProviderAccountingUnknownProjection(t *testing.T) {
	t.Parallel()
	r := recordedAccountingFixture()
	r.Usage.ReasoningTokens, r.Usage.ReasoningTokensReported = -1, false
	r.PricingUsage.Input.Total = llm.TokenQuantity{Tokens: math.MinInt64}
	r.PricingUsage.Input.WritesByLifetime[0].Quantity = llm.TokenQuantity{Tokens: -77}
	r.RateCard.FreshInput = llm.TokenRate{PerMillionTokens: math.NaN()}
	r.RateCard.CacheRead = llm.TokenRate{PerMillionTokens: math.Inf(1)}
	r.RateCard.Output = llm.TokenRate{PerMillionTokens: -1}
	r.RateCard.CacheWrites[0].Rate = llm.TokenRate{PerMillionTokens: math.Inf(-1)}
	r.ConfiguredEstimate.HasKnownSubtotal = false
	r.ConfiguredEstimate.Subtotal = math.NaN()
	r.ReportedCost = &llm.ReportedCost{Amount: math.Inf(1), Currency: "", CoverageKnown: true}
	got := decodeRecordedAccounting(t, encodeRecordedAccounting(t, r))
	if got.Usage.ReasoningTokensReported || got.Usage.ReasoningTokens != 0 ||
		got.PricingUsage.Input.Total != (llm.TokenQuantity{}) || got.PricingUsage.Input.WritesByLifetime[0].Quantity != (llm.TokenQuantity{}) ||
		got.RateCard.FreshInput != (llm.TokenRate{}) || got.RateCard.CacheRead != (llm.TokenRate{}) || got.RateCard.Output != (llm.TokenRate{}) ||
		got.RateCard.CacheWrites[0].Rate != (llm.TokenRate{}) || got.ConfiguredEstimate.HasKnownSubtotal || got.ConfiguredEstimate.Subtotal != 0 ||
		got.ReportedCost.Reported || got.ReportedCost.Amount != 0 || !got.ReportedCost.CoverageKnown {
		t.Fatal("undefined scalar garbage became known numeric evidence")
	}
}

func TestRecordedProviderAccountingFrozenEstimates(t *testing.T) {
	t.Parallel()
	r := recordedAccountingFixture()
	// A persisted result may predate today's estimator policy. Do not derive it
	// from card+usage, even when its numbers differ from the current estimator.
	r.ConfiguredEstimate.InputAmount, r.ConfiguredEstimate.OutputAmount, r.ConfiguredEstimate.Subtotal = 23, 41, 64
	want := r.Clone()
	data := encodeRecordedAccounting(t, r)
	r.RateCard.Reference.Revision = "new-policy"
	r.RateCard.Output.PerMillionTokens = 999
	r.PricingUsage.Output.Total.Tokens = 100
	later := backends.EstimatePrice(*r.RateCard, *r.PricingUsage)
	if later.Subtotal == want.ConfiguredEstimate.Subtotal {
		t.Fatal("fixture does not distinguish the later policy")
	}
	got := decodeRecordedAccounting(t, data)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("restored evidence was repriced or aliased")
	}
	// The real estimator retains valid direction amounts when only the final
	// subtotal overflows. HasKnownSubtotal=false does not erase those amounts.
	card := *want.RateCard
	card.TokenOnly = true
	card.FreshInput.PerMillionTokens, card.Output.PerMillionTokens = math.MaxFloat64, math.MaxFloat64
	usage := llm.PricingUsage{
		Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 1_000_000, Known: true},
			CacheRead: llm.TokenQuantity{Known: true}, CacheWrite: llm.TokenQuantity{Known: true}, Other: llm.TokenQuantity{Known: true}, PartitionKnown: true},
		Output: llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 1_000_000, Known: true}, Other: llm.TokenQuantity{Known: true}},
	}
	overflow := backends.EstimatePrice(card, usage)
	if overflow.HasKnownSubtotal || overflow.InputAmount != math.MaxFloat64 || overflow.OutputAmount != math.MaxFloat64 {
		t.Fatalf("invalid subtotal-overflow fixture: %+v", overflow)
	}
	want.RateCard, want.PricingUsage, want.ConfiguredEstimate = &card, &usage, &overflow
	got = decodeRecordedAccounting(t, encodeRecordedAccounting(t, want))
	if !reflect.DeepEqual(got, want) {
		t.Fatal("subtotal overflow erased independently calculated direction amounts")
	}
}

func TestRecordedProviderAccountingCloneOwnership(t *testing.T) {
	t.Parallel()
	r := recordedAccountingFixture()
	r.ReportedCost.Source = "in-memory source remains in Clone, never in storage"
	clone := r.Clone()
	if !reflect.DeepEqual(clone, r) {
		t.Fatal("Clone changed domain evidence")
	}
	wireWant := r.Clone()
	wireWant.ReportedCost.Source = ""
	data := encodeRecordedAccounting(t, r)
	r.Usage.PromptTokens++
	r.PricingUsage.Input.Total.Tokens++
	r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens++
	r.RateCard.Reference.ID = "changed"
	r.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	r.ConfiguredEstimate.Subtotal++
	r.ConfiguredEstimate.Issues[0].Field = "changed"
	r.ReportedCost.Amount++
	r.ReportedCost.Source = "changed"
	if clone.Usage.PromptTokens != wireWant.Usage.PromptTokens ||
		!reflect.DeepEqual(clone.PricingUsage, wireWant.PricingUsage) || !reflect.DeepEqual(clone.RateCard, wireWant.RateCard) ||
		!reflect.DeepEqual(clone.ConfiguredEstimate, wireWant.ConfiguredEstimate) || clone.ReportedCost.Amount != wireWant.ReportedCost.Amount ||
		clone.ReportedCost.Source == r.ReportedCost.Source {
		t.Fatal("Clone retained mutable nested aliases")
	}
	got := decodeRecordedAccounting(t, data)
	if !reflect.DeepEqual(got, wireWant) {
		t.Fatal("Encode borrowed mutable snapshots")
	}
	second := decodeRecordedAccounting(t, data)
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens++
	got.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	got.ConfiguredEstimate.Issues[0].Field = "mutated decoded snapshot"
	clear(data)
	if !reflect.DeepEqual(second, wireWant) {
		t.Fatal("Decode borrowed input bytes or shared nested snapshots")
	}
}

func TestRecordedProviderAccountingCanaryAllowlist(t *testing.T) {
	t.Parallel()
	const canary = "PRIVATE-CANARY-DO-NOT-PERSIST"
	// Surround allowed evidence with the excluded execution/request graph. Only
	// the cost projection crosses this codec's public boundary, never Execution.
	graph := struct {
		Execution               llm.ExecutionObservation
		Headers                 map[string]string
		Prompt, URL, Credential string
		Err                     error
		Accounting              engine.RecordedProviderAccounting
	}{
		Execution: llm.ExecutionObservation{Route: canary, RequestedModel: canary, GatewayDeployment: canary, GatewayVersion: canary,
			ReportedModel: canary, ReportedVersion: canary, GatewayCallID: canary, ProviderCallID: canary,
			ReportedCost: llm.ReportedCost{Amount: 7, Currency: "EUR", Reported: true, Source: canary}},
		Headers: map[string]string{"Authorization": canary}, Prompt: canary, URL: "https://" + canary, Credential: canary,
		Err: errors.New(canary), Accounting: recordedAccountingFixture(),
	}
	graph.Accounting.ReportedCost = &graph.Execution.ReportedCost
	data := encodeRecordedAccounting(t, graph.Accounting)
	if bytes.Contains(data, []byte(canary)) {
		t.Fatal("private canary escaped the storage allowlist")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 12 {
		t.Fatalf("unexpected top-level storage fields: %d", len(fields))
	}
	for _, key := range []string{"version", "request_batch_id", "task_id", "attempt", "provider", "model", "usage", "pricing_usage", "rate_card", "estimate_availability", "configured_estimate", "reported_cost"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing allowed field %q", key)
		}
	}
	var cost map[string]json.RawMessage
	if err := json.Unmarshal(fields["reported_cost"], &cost); err != nil {
		t.Fatal(err)
	}
	if len(cost) != 4 || cost["amount"] == nil || cost["currency"] == nil || cost["reported"] == nil || cost["coverage_known"] == nil {
		t.Fatal("reported-cost DTO includes nonallowlisted fields")
	}
	decoded := decodeRecordedAccounting(t, data)
	if decoded.ReportedCost.Source != "" || decoded.ReportedCost.Amount != 7 || decoded.ReportedCost.Currency != "EUR" {
		t.Fatal("cost projection changed allowed fields or retained Source")
	}
	// Source is omitted, not validated, retained or copied into diagnostics.
	graph.Accounting.ReportedCost.Source = string([]byte{0xff})
	if !bytes.Equal(data, encodeRecordedAccounting(t, graph.Accounting)) {
		t.Fatal("excluded Source influenced stored bytes")
	}
}
