package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

const auxiliaryInvocationUUID = "12345678-1234-4234-9234-123456789abc"

func recordedAuxiliaryAccountingFixture() engine.RecordedSessionAuxiliaryAccounting {
	r := recordedAccountingFixture()
	return engine.RecordedSessionAuxiliaryAccounting{
		OwnerRequestBatchID: r.RequestBatchID, InvocationID: auxiliaryInvocationUUID,
		Purpose: engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE, StreamOutcome: inference.StreamOutcomes.COMPLETED,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

func encodeRecordedAuxiliaryAccounting(t *testing.T, r engine.RecordedSessionAuxiliaryAccounting) []byte {
	t.Helper()
	data, err := engine.EncodeRecordedSessionAuxiliaryAccounting(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeRecordedAuxiliaryAccounting(t *testing.T, data []byte) engine.RecordedSessionAuxiliaryAccounting {
	t.Helper()
	r, err := engine.DecodeRecordedSessionAuxiliaryAccounting(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordedSessionAuxiliaryAccountingRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedSessionAuxiliaryAccounting)
	}{
		{name: "partial non USD mixed cache lifetime coverage"},
		{name: "unknown currency and billing", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = "", "", ""
			r.RateCard.Billing, r.ConfiguredEstimate.Billing = llm.BillingMode{}, llm.BillingMode{}
		}},
		{name: "unknown partition and independent output", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.PricingUsage.Input.PartitionKnown = false
			r.PricingUsage.Input.CacheRead = llm.TokenQuantity{}
		}},
		{name: "open currencies and labels", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.Provider, r.Model = "", ""
			r.RateCard.Currency, r.ConfiguredEstimate.Currency, r.ReportedCost.Currency = " X-Credit ", " X-Credit ", " X-Credit "
		}},
		{name: "integer and duration limits stay exact", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.Usage.TotalTokens = math.MaxInt
			r.PricingUsage.Input.Total.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = math.MaxInt64
			r.PricingUsage.Input.WritesByLifetime[0].Lifetime = time.Duration(math.MaxInt64)
			r.RateCard.CacheWrites[0].Lifetime = time.Duration(math.MaxInt64)
			r.ConfiguredEstimate.Issues[0].Lifetime = time.Duration(math.MaxInt64)
		}},
		{name: "nil slices", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.PricingUsage.Input.WritesByLifetime, r.RateCard.CacheWrites, r.ConfiguredEstimate.Issues = nil, nil, nil
		}},
		{name: "present empty slices", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.PricingUsage.Input.WritesByLifetime = []llm.CacheWriteQuantity{}
			r.RateCard.CacheWrites = []llm.CacheWriteRate{}
			r.ConfiguredEstimate.Issues = []llm.PricingIssue{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := recordedAuxiliaryAccountingFixture()
			if tc.edit != nil {
				tc.edit(&want)
			}
			got := decodeRecordedAuxiliaryAccounting(t, encodeRecordedAuxiliaryAccounting(t, want))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed evidence: got %+v; want %+v", got, want)
			}
		})
	}
}

func TestRecordedSessionAuxiliaryAccountingPresenceAndAvailability(t *testing.T) {
	t.Parallel()
	// Exercise every optional-pointer combination, including frozen estimates
	// whose supporting snapshots are absent. Availability is not recomputation.
	for mask := range 32 {
		r := engine.RecordedSessionAuxiliaryAccounting{OwnerRequestBatchID: "r", InvocationID: auxiliaryInvocationUUID, Purpose: engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE, StreamOutcome: inference.StreamOutcomes.COMPLETED}
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
		data := encodeRecordedAuxiliaryAccounting(t, r)
		got := decodeRecordedAuxiliaryAccounting(t, data)
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
	zero := recordedAuxiliaryAccountingFixture()
	zero.PricingUsage = &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}
	zero.RateCard = &llm.RateCard{FreshInput: llm.TokenRate{Known: true}}
	zero.ConfiguredEstimate = &llm.PriceEstimate{HasKnownSubtotal: true, Complete: true}
	zero.ReportedCost = &llm.ReportedCost{Reported: true, CoverageKnown: true}
	if got := decodeRecordedAuxiliaryAccounting(t, encodeRecordedAuxiliaryAccounting(t, zero)); !reflect.DeepEqual(got, zero) {
		t.Fatal("known zero became absent or unknown")
	}
}

func TestRecordedSessionAuxiliaryAccountingUnknownProjection(t *testing.T) {
	t.Parallel()
	r := recordedAuxiliaryAccountingFixture()
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
	got := decodeRecordedAuxiliaryAccounting(t, encodeRecordedAuxiliaryAccounting(t, r))
	if got.Usage.ReasoningTokensReported || got.Usage.ReasoningTokens != 0 ||
		got.PricingUsage.Input.Total != (llm.TokenQuantity{}) || got.PricingUsage.Input.WritesByLifetime[0].Quantity != (llm.TokenQuantity{}) ||
		got.RateCard.FreshInput != (llm.TokenRate{}) || got.RateCard.CacheRead != (llm.TokenRate{}) || got.RateCard.Output != (llm.TokenRate{}) ||
		got.RateCard.CacheWrites[0].Rate != (llm.TokenRate{}) || got.ConfiguredEstimate.HasKnownSubtotal || got.ConfiguredEstimate.Subtotal != 0 ||
		got.ReportedCost.Reported || got.ReportedCost.Amount != 0 || !got.ReportedCost.CoverageKnown {
		t.Fatal("undefined scalar garbage became known numeric evidence")
	}
}

func TestRecordedSessionAuxiliaryAccountingFrozenEstimates(t *testing.T) {
	t.Parallel()
	r := recordedAuxiliaryAccountingFixture()
	// A persisted result may predate today's estimator policy. Do not derive it
	// from card+usage, even when its numbers differ from the current estimator.
	r.ConfiguredEstimate.InputAmount, r.ConfiguredEstimate.OutputAmount, r.ConfiguredEstimate.Subtotal = 23, 41, 64
	want := r.Clone()
	data := encodeRecordedAuxiliaryAccounting(t, r)
	r.RateCard.Reference.Revision = "new-policy"
	r.RateCard.Output.PerMillionTokens = 999
	r.PricingUsage.Output.Total.Tokens = 100
	later := backends.EstimatePrice(*r.RateCard, *r.PricingUsage)
	if later.Subtotal == want.ConfiguredEstimate.Subtotal {
		t.Fatal("fixture does not distinguish the later policy")
	}
	got := decodeRecordedAuxiliaryAccounting(t, data)
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
	got = decodeRecordedAuxiliaryAccounting(t, encodeRecordedAuxiliaryAccounting(t, want))
	if !reflect.DeepEqual(got, want) {
		t.Fatal("subtotal overflow erased independently calculated direction amounts")
	}
}

func TestRecordedSessionAuxiliaryAccountingCloneOwnership(t *testing.T) {
	t.Parallel()
	r := recordedAuxiliaryAccountingFixture()
	r.ReportedCost.Source = "in-memory source remains in Clone, never in storage"
	clone := r.Clone()
	if !reflect.DeepEqual(clone, r) {
		t.Fatal("Clone changed domain evidence")
	}
	wireWant := r.Clone()
	wireWant.ReportedCost.Source = ""
	data := encodeRecordedAuxiliaryAccounting(t, r)
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
	got := decodeRecordedAuxiliaryAccounting(t, data)
	if !reflect.DeepEqual(got, wireWant) {
		t.Fatal("Encode borrowed mutable snapshots")
	}
	second := decodeRecordedAuxiliaryAccounting(t, data)
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens++
	got.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	got.ConfiguredEstimate.Issues[0].Field = "mutated decoded snapshot"
	clear(data)
	if !reflect.DeepEqual(second, wireWant) {
		t.Fatal("Decode borrowed input bytes or shared nested snapshots")
	}
}

func TestRecordedSessionAuxiliaryAccountingCanaryAllowlist(t *testing.T) {
	t.Parallel()
	const canary = "PRIVATE-CANARY-DO-NOT-PERSIST"
	// Surround allowed evidence with the excluded execution/request graph. Only
	// the cost projection crosses this codec's public boundary, never Execution.
	graph := struct {
		Execution               llm.ExecutionObservation
		Headers                 map[string]string
		Prompt, URL, Credential string
		Err                     error
		Accounting              engine.RecordedSessionAuxiliaryAccounting
	}{
		Execution: llm.ExecutionObservation{Route: canary, RequestedModel: canary, GatewayDeployment: canary, GatewayVersion: canary,
			ReportedModel: canary, ReportedVersion: canary, GatewayCallID: canary, ProviderCallID: canary,
			ReportedCost: llm.ReportedCost{Amount: 7, Currency: "EUR", Reported: true, Source: canary}},
		Headers: map[string]string{"Authorization": canary}, Prompt: canary, URL: "https://" + canary, Credential: canary,
		Err: errors.New(canary), Accounting: recordedAuxiliaryAccountingFixture(),
	}
	graph.Accounting.ReportedCost = &graph.Execution.ReportedCost
	data := encodeRecordedAuxiliaryAccounting(t, graph.Accounting)
	if bytes.Contains(data, []byte(canary)) {
		t.Fatal("private canary escaped the storage allowlist")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 13 {
		t.Fatalf("unexpected top-level storage fields: %d", len(fields))
	}
	for _, key := range []string{"version", "owner_request_batch_id", "invocation_id", "purpose", "stream_outcome", "provider", "model", "usage", "pricing_usage", "rate_card", "estimate_availability", "configured_estimate", "reported_cost"} {
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
	decoded := decodeRecordedAuxiliaryAccounting(t, data)
	if decoded.ReportedCost.Source != "" || decoded.ReportedCost.Amount != 7 || decoded.ReportedCost.Currency != "EUR" {
		t.Fatal("cost projection changed allowed fields or retained Source")
	}
	// Source is omitted, not validated, retained or copied into diagnostics.
	graph.Accounting.ReportedCost.Source = string([]byte{0xff})
	if !bytes.Equal(data, encodeRecordedAuxiliaryAccounting(t, graph.Accounting)) {
		t.Fatal("excluded Source influenced stored bytes")
	}
}

func TestRecordedSessionAuxiliaryAccountingEveryPurposeAndOutcome(t *testing.T) {
	t.Parallel()
	purposes := make(map[string]bool)
	outcomes := make(map[string]bool)
	for purpose := range engine.AuxiliaryAccountingPurposes.All() {
		purposes[purpose.String()] = true
		for outcome := range inference.StreamOutcomes.All() {
			outcomes[outcome.String()] = true
			r := recordedAuxiliaryAccountingFixture()
			r.Purpose, r.StreamOutcome = purpose, outcome
			if got := decodeRecordedAuxiliaryAccounting(t, encodeRecordedAuxiliaryAccounting(t, r)); !reflect.DeepEqual(got, r) {
				t.Fatal("purpose/outcome changed")
			}
		}
	}
	if !reflect.DeepEqual(purposes, map[string]bool{"judge": true, "spawn_planner": true, "summary": true, "executive": true, "handover": true}) {
		t.Fatal("purpose vocabulary changed")
	}
	if !reflect.DeepEqual(outcomes, map[string]bool{"completed": true, "failed": true, "cancelled": true, "stopped": true, "interrupted": true}) {
		t.Fatal("outcome vocabulary changed")
	}
}

func TestReadRecordedSessionAuxiliaryAccountingEnvelope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedSessionAuxiliaryAccounting)
		wire func([]byte) []byte
		want error
	}{
		{name: "valid"},
		{name: "invocation mismatch", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) {
			r.InvocationID = "abcdefab-1234-4234-9234-123456789abc"
		}, want: engine.ErrInvalidRecordedSessionAuxiliaryAccounting},
		{name: "owner mismatch", edit: func(r *engine.RecordedSessionAuxiliaryAccounting) { r.OwnerRequestBatchID = "other-owner" }, want: engine.ErrInvalidRecordedSessionAuxiliaryAccounting},
		{name: "codec version", wire: func(data []byte) []byte { return bytes.Replace(data, []byte(`"version":1`), []byte(`"version":2`), 1) }, want: engine.ErrRecordedSessionAuxiliaryAccountingVersion},
		{name: "safe parser error", wire: func([]byte) []byte { return []byte(`{"PRIVATE-CANARY":1}`) }, want: engine.ErrInvalidRecordedSessionAuxiliaryAccounting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "reader.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			r := recordedAuxiliaryAccountingFixture()
			invocation, owner := r.InvocationID, r.OwnerRequestBatchID
			if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "session", Workspace: "/workspace"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO session_history_batches (session_id, batch_id, checksum) VALUES (?, ?, ?)", "session", owner, "reader-receipt"); err != nil {
				t.Fatal(err)
			}
			if tc.edit != nil {
				tc.edit(&r)
			}
			data := encodeRecordedAuxiliaryAccounting(t, r)
			if tc.wire != nil {
				data = tc.wire(data)
			}
			if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "session", []db.SessionAuxiliaryProviderAccounting{{InvocationID: invocation, OwnerRequestBatchID: owner, Version: 1, PayloadJSON: data}}); err != nil {
				t.Fatal(err)
			}
			got, err := engine.ReadRecordedSessionAuxiliaryAccounting(t.Context(), store, "session", invocation)
			if !errors.Is(err, tc.want) {
				t.Fatalf("read: %v want %v", err, tc.want)
			}
			if tc.want == nil && !bytes.Equal(data, encodeRecordedAuxiliaryAccounting(t, got)) {
				t.Fatal("reader changed evidence")
			}
			if tc.want != nil && (got.InvocationID != "" || errors.Unwrap(err) != nil) {
				t.Fatal("partial evidence or unsafe codec chain")
			}
			if _, err := engine.ReadRecordedSessionAuxiliaryAccounting(t.Context(), store, "absent", invocation); !errors.Is(err, db.ErrNotFound) {
				t.Fatal("missing", err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_auxiliary_provider_accounting SET checksum='wrong'"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadRecordedSessionAuxiliaryAccounting(t.Context(), store, "session", invocation); !errors.Is(err, db.ErrAuxiliaryProviderAccountingCorrupt) {
				t.Fatal("corrupt", err)
			}
		})
	}
}
