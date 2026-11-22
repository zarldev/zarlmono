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

func recordedHeadlessAccountingFixture() engine.RecordedHeadlessProviderAccounting {
	r := recordedAccountingFixture()
	return engine.RecordedHeadlessProviderAccounting{
		RunID: "opaque run", PursueAttempt: 2, ProviderOrdinal: 3,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

func encodeRecordedHeadlessAccounting(t *testing.T, r engine.RecordedHeadlessProviderAccounting) []byte {
	t.Helper()
	data, err := engine.EncodeRecordedHeadlessProviderAccounting(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeRecordedHeadlessAccounting(t *testing.T, data []byte) engine.RecordedHeadlessProviderAccounting {
	t.Helper()
	r, err := engine.DecodeRecordedHeadlessProviderAccounting(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordedHeadlessProviderAccountingRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedHeadlessProviderAccounting)
	}{
		{name: "partial non USD mixed cache lifetime coverage"},
		{name: "unknown currency and billing", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = "", "", ""
			r.RateCard.Billing, r.ConfiguredEstimate.Billing = llm.BillingMode{}, llm.BillingMode{}
		}},
		{name: "unknown partition and independent output", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.PricingUsage.Input.PartitionKnown = false
			r.PricingUsage.Input.CacheRead = llm.TokenQuantity{}
		}},
		{name: "open currencies and labels", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.Provider, r.Model = "", ""
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = " X-Credit ", " X-Credit ", " X-Credit "
		}},
		{name: "integer and duration limits stay exact", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.PursueAttempt, r.Usage.TotalTokens = math.MaxInt, math.MaxInt
			r.PricingUsage.Input.Total.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Lifetime = time.Duration(math.MaxInt64)
			r.RateCard.CacheWrites[0].Lifetime = time.Duration(math.MaxInt64)
			r.ConfiguredEstimate.Issues[0].Lifetime = time.Duration(math.MaxInt64)
		}},
		{name: "nil slices", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.PricingUsage.Input.WritesByLifetime, r.RateCard.CacheWrites, r.ConfiguredEstimate.Issues = nil, nil, nil
		}},
		{name: "present empty slices", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.PricingUsage.Input.WritesByLifetime = []llm.CacheWriteQuantity{}
			r.RateCard.CacheWrites = []llm.CacheWriteRate{}
			r.ConfiguredEstimate.Issues = []llm.PricingIssue{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := recordedHeadlessAccountingFixture()
			if tc.edit != nil {
				tc.edit(&want)
			}
			got := decodeRecordedHeadlessAccounting(t, encodeRecordedHeadlessAccounting(t, want))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed evidence: got %+v; want %+v", got, want)
			}
		})
	}
}

func TestRecordedHeadlessProviderAccountingPresenceAndAvailability(t *testing.T) {
	t.Parallel()
	// Exercise every optional-pointer combination, including frozen estimates
	// whose supporting snapshots are absent. Availability is not recomputation.
	for mask := range 32 {
		r := engine.RecordedHeadlessProviderAccounting{RunID: "r", PursueAttempt: 1, ProviderOrdinal: 1}
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
		data := encodeRecordedHeadlessAccounting(t, r)
		got := decodeRecordedHeadlessAccounting(t, data)
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
	zero := recordedHeadlessAccountingFixture()
	zero.PricingUsage = &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}
	zero.RateCard = &llm.RateCard{FreshInput: llm.TokenRate{Known: true}}
	zero.ConfiguredEstimate = &llm.PriceEstimate{HasKnownSubtotal: true, Complete: true}
	zero.ReportedCost = &llm.ReportedCost{Reported: true, CoverageKnown: true}
	if got := decodeRecordedHeadlessAccounting(t, encodeRecordedHeadlessAccounting(t, zero)); !reflect.DeepEqual(got, zero) {
		t.Fatal("known zero became absent or unknown")
	}
}

func TestRecordedHeadlessProviderAccountingUnknownProjection(t *testing.T) {
	t.Parallel()
	r := recordedHeadlessAccountingFixture()
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
	got := decodeRecordedHeadlessAccounting(t, encodeRecordedHeadlessAccounting(t, r))
	if got.Usage.ReasoningTokensReported || got.Usage.ReasoningTokens != 0 ||
		got.PricingUsage.Input.Total != (llm.TokenQuantity{}) || got.PricingUsage.Input.WritesByLifetime[0].Quantity != (llm.TokenQuantity{}) ||
		got.RateCard.FreshInput != (llm.TokenRate{}) || got.RateCard.CacheRead != (llm.TokenRate{}) || got.RateCard.Output != (llm.TokenRate{}) ||
		got.RateCard.CacheWrites[0].Rate != (llm.TokenRate{}) || got.ConfiguredEstimate.HasKnownSubtotal || got.ConfiguredEstimate.Subtotal != 0 ||
		got.ReportedCost.Reported || got.ReportedCost.Amount != 0 || !got.ReportedCost.CoverageKnown {
		t.Fatal("undefined scalar garbage became known numeric evidence")
	}
}

func TestRecordedHeadlessProviderAccountingFrozenEstimates(t *testing.T) {
	t.Parallel()
	r := recordedHeadlessAccountingFixture()
	// A persisted result may predate today's estimator policy. Do not derive it
	// from card+usage, even when its numbers differ from the current estimator.
	r.ConfiguredEstimate.InputAmount, r.ConfiguredEstimate.OutputAmount, r.ConfiguredEstimate.Subtotal = 23, 41, 64
	want := r.Clone()
	data := encodeRecordedHeadlessAccounting(t, r)
	r.RateCard.Reference.Revision = "new-policy"
	r.RateCard.Output.PerMillionTokens = 999
	r.PricingUsage.Output.Total.Tokens = 100
	later := backends.EstimatePrice(*r.RateCard, *r.PricingUsage)
	if later.Subtotal == want.ConfiguredEstimate.Subtotal {
		t.Fatal("fixture does not distinguish the later policy")
	}
	got := decodeRecordedHeadlessAccounting(t, data)
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
	got = decodeRecordedHeadlessAccounting(t, encodeRecordedHeadlessAccounting(t, want))
	if !reflect.DeepEqual(got, want) {
		t.Fatal("subtotal overflow erased independently calculated direction amounts")
	}
}

func TestRecordedHeadlessProviderAccountingCloneOwnership(t *testing.T) {
	t.Parallel()
	r := recordedHeadlessAccountingFixture()
	r.ReportedCost.Source = "in-memory source remains in Clone, never in storage"
	clone := r.Clone()
	if !reflect.DeepEqual(clone, r) {
		t.Fatal("Clone changed domain evidence")
	}
	wireWant := r.Clone()
	wireWant.ReportedCost.Source = ""
	data := encodeRecordedHeadlessAccounting(t, r)
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
	got := decodeRecordedHeadlessAccounting(t, data)
	if !reflect.DeepEqual(got, wireWant) {
		t.Fatal("Encode borrowed mutable snapshots")
	}
	second := decodeRecordedHeadlessAccounting(t, data)
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens++
	got.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	got.ConfiguredEstimate.Issues[0].Field = "mutated decoded snapshot"
	clear(data)
	if !reflect.DeepEqual(second, wireWant) {
		t.Fatal("Decode borrowed input bytes or shared nested snapshots")
	}
}

func TestRecordedHeadlessProviderAccountingCanaryAllowlist(t *testing.T) {
	t.Parallel()
	const canary = "PRIVATE-CANARY-DO-NOT-PERSIST"
	// Surround allowed evidence with the excluded execution/request graph. Only
	// the cost projection crosses this codec's public boundary, never Execution.
	graph := struct {
		Execution               llm.ExecutionObservation
		Headers                 map[string]string
		Prompt, URL, Credential string
		Err                     error
		Accounting              engine.RecordedHeadlessProviderAccounting
	}{
		Execution: llm.ExecutionObservation{Route: canary, RequestedModel: canary, GatewayDeployment: canary, GatewayVersion: canary,
			ReportedModel: canary, ReportedVersion: canary, GatewayCallID: canary, ProviderCallID: canary,
			ReportedCost: llm.ReportedCost{Amount: 7, Currency: "EUR", Reported: true, Source: canary}},
		Headers: map[string]string{"Authorization": canary}, Prompt: canary, URL: "https://" + canary, Credential: canary,
		Err: errors.New(canary), Accounting: recordedHeadlessAccountingFixture(),
	}
	graph.Accounting.ReportedCost = &graph.Execution.ReportedCost
	data := encodeRecordedHeadlessAccounting(t, graph.Accounting)
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
	for _, key := range []string{"version", "run_id", "pursue_attempt", "provider_ordinal", "provider", "model", "usage", "pricing_usage", "rate_card", "estimate_availability", "configured_estimate", "reported_cost"} {
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
	decoded := decodeRecordedHeadlessAccounting(t, data)
	if decoded.ReportedCost.Source != "" || decoded.ReportedCost.Amount != 7 || decoded.ReportedCost.Currency != "EUR" {
		t.Fatal("cost projection changed allowed fields or retained Source")
	}
	// Source is omitted, not validated, retained or copied into diagnostics.
	graph.Accounting.ReportedCost.Source = string([]byte{0xff})
	if !bytes.Equal(data, encodeRecordedHeadlessAccounting(t, graph.Accounting)) {
		t.Fatal("excluded Source influenced stored bytes")
	}
}
