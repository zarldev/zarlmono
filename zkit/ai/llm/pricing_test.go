package llm_test

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestPricingValuePresenceAndClones(t *testing.T) {
	var usage llm.PricingUsage
	var card llm.RateCard
	var estimate llm.PriceEstimate
	var reported llm.ReportedCost
	if usage.Input.Total.Known || usage.Output.Total.Known || card.FreshInput.Known ||
		estimate.HasKnownSubtotal || estimate.Complete || reported.Reported || reported.CoverageKnown {
		t.Fatal("zero values must not assert known quantities, rates, estimates or reports")
	}
	if usage.Clone().Input.WritesByLifetime != nil || card.Clone().CacheWrites != nil || estimate.Clone().Issues != nil {
		t.Fatal("cloning absence allocated collections")
	}
	usage.Input.WritesByLifetime = []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true}}}
	card.CacheWrites = []llm.CacheWriteRate{{Lifetime: time.Hour, Rate: llm.TokenRate{Known: true}}}
	estimate.Issues = []llm.PricingIssue{{Kind: llm.PricingIssueKinds.PRICINGUNKNOWNRATE, Field: "input", Lifetime: time.Hour}}
	usageClone, cardClone, estimateClone := usage.Clone(), card.Clone(), estimate.Clone()
	if !slices.Equal(usageClone.Input.WritesByLifetime, usage.Input.WritesByLifetime) ||
		!slices.Equal(cardClone.CacheWrites, card.CacheWrites) || !slices.Equal(estimateClone.Issues, estimate.Issues) {
		t.Fatal("clone changed original values")
	}
	usageClone.Input.WritesByLifetime[0].Quantity.Tokens = 12
	cardClone.CacheWrites[0].Rate.PerMillionTokens = 8
	estimateClone.Issues[0].Field = "changed"
	if usage.Input.WritesByLifetime[0].Quantity.Tokens != 0 || card.CacheWrites[0].Rate.PerMillionTokens != 0 || estimate.Issues[0].Field != "input" {
		t.Fatal("clone mutations reached original")
	}
	usage.Input.WritesByLifetime[0].Lifetime = time.Minute
	card.CacheWrites[0].Lifetime = time.Minute
	estimate.Issues[0].Lifetime = time.Minute
	if usageClone.Input.WritesByLifetime[0].Lifetime != time.Hour || cardClone.CacheWrites[0].Lifetime != time.Hour || estimateClone.Issues[0].Lifetime != time.Hour {
		t.Fatal("original mutations reached clone")
	}
	reported = llm.ReportedCost{Amount: 0, Currency: "opaque-unit", Reported: true, Source: "report-4"}
	copyOfReport := reported
	if copyOfReport != reported || !copyOfReport.Reported || copyOfReport.CoverageKnown || copyOfReport.Amount != 0 {
		t.Fatal("reported zero with unknown coverage was not retained separately")
	}
}

func TestPricingEnumContracts(t *testing.T) {
	t.Run("billing", func(t *testing.T) {
		checkPricingEnum[llm.BillingMode](t, []llm.BillingMode{
			llm.BillingModes.BILLINGUNKNOWN, llm.BillingModes.BILLINGTOKENMETERED,
			llm.BillingModes.BILLINGSUBSCRIPTION, llm.BillingModes.BILLINGUNMETERED,
		}, []string{"unknown", "token-metered", "subscription", "unmetered"}, llm.ParseBillingMode, llm.ErrParseBillingMode)
	})
	t.Run("issues", func(t *testing.T) {
		checkPricingEnum[llm.PricingIssueKind](t, []llm.PricingIssueKind{
			llm.PricingIssueKinds.PRICINGISSUEUNKNOWN, llm.PricingIssueKinds.PRICINGUNKNOWNBILLING,
			llm.PricingIssueKinds.PRICINGUNKNOWNBASIS, llm.PricingIssueKinds.PRICINGUNRESOLVEDAPPLICABILITY,
			llm.PricingIssueKinds.PRICINGUNKNOWNQUANTITY, llm.PricingIssueKinds.PRICINGUNKNOWNRATE,
			llm.PricingIssueKinds.PRICINGINCONSISTENTSUBSETS, llm.PricingIssueKinds.PRICINGUNKNOWNWRITELIFETIME,
			llm.PricingIssueKinds.PRICINGUNSUPPORTEDDIMENSIONS, llm.PricingIssueKinds.PRICINGARITHMETICOVERFLOW,
		}, []string{"unknown", "unknown-billing", "unknown-basis", "unresolved-applicability", "unknown-quantity", "unknown-rate",
			"inconsistent-subsets", "unknown-write-lifetime", "unsupported-dimensions", "arithmetic-overflow"}, llm.ParsePricingIssueKind, llm.ErrParsePricingIssueKind)
	})
}

type pricingEnumValue interface {
	comparable
	IsValid() bool
	MarshalText() ([]byte, error)
	MarshalBinary() ([]byte, error)
}

func checkPricingEnum[E pricingEnumValue, P interface {
	*E
	UnmarshalText([]byte) error
	UnmarshalBinary([]byte) error
}](t *testing.T, values []E, names []string, parse func(any) (E, error), parseErr error) {
	t.Helper()
	for number, value := range values {
		t.Run(names[number], func(t *testing.T) {
			if !value.IsValid() {
				t.Fatalf("value = %v, want valid %q", value, names[number])
			}
			for _, input := range []any{number, int64(number), uint64(number), float64(number), names[number], []byte(names[number]), value} {
				got, err := parse(input)
				if err != nil || got != value {
					t.Fatalf("parse(%v [%T]) = %v, %v; want %v", input, input, got, err, value)
				}
			}
			decoded := values[len(values)-1]
			text, err := value.MarshalText()
			if err != nil || string(text) != names[number] {
				t.Fatalf("text = %q, %v", text, err)
			}
			if err := P(&decoded).UnmarshalText(text); err != nil || decoded != value {
				t.Fatalf("text round-trip = %v, %v", decoded, err)
			}
			binary, err := value.MarshalBinary()
			if err != nil || string(binary) != names[number] {
				t.Fatalf("binary = %q, %v", binary, err)
			}
			decoded = values[len(values)-1]
			if err := P(&decoded).UnmarshalBinary(binary); err != nil || decoded != value {
				t.Fatalf("binary round-trip = %v, %v", decoded, err)
			}
			if err := P(&decoded).UnmarshalBinary([]byte(`"` + names[number] + `"`)); err != nil || decoded != value {
				t.Fatalf("legacy binary = %v, %v", decoded, err)
			}
			if err := P(&decoded).UnmarshalText([]byte("invalid-pricing-value")); !errors.Is(err, parseErr) || decoded != value {
				t.Fatalf("invalid text changed receiver or error identity: %v, %v", decoded, err)
			}
			if err := P(&decoded).UnmarshalBinary([]byte("invalid-pricing-value")); !errors.Is(err, parseErr) || decoded != value {
				t.Fatalf("invalid binary changed receiver or error identity: %v, %v", decoded, err)
			}
			want := map[E]E{value: value}
			encoded, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("marshal map: %v", err)
			}
			var wire map[string]string
			if err := json.Unmarshal(encoded, &wire); err != nil || !maps.Equal(wire, map[string]string{names[number]: names[number]}) {
				t.Fatalf("canonical map = %s, %v", encoded, err)
			}
			var got map[E]E
			if err := json.Unmarshal(encoded, &got); err != nil || !maps.Equal(got, want) {
				t.Fatalf("map round-trip = %v, %v", got, err)
			}
			if err := json.Unmarshal([]byte(`{"invalid-pricing-value":"unknown"}`), &got); !errors.Is(err, parseErr) {
				t.Fatalf("invalid map key error = %v", err)
			}
		})
	}
	invalid, err := parse(-1)
	if !errors.Is(err, parseErr) || invalid.IsValid() {
		t.Fatalf("invalid parse = %v, %v", invalid, err)
	}
	for _, input := range []any{invalid, nil, true, struct{}{}, "", "TOKEN-METERED", 0.5, math.NaN(), math.Inf(1), uint64(math.MaxUint64)} {
		got, err := parse(input)
		if !errors.Is(err, parseErr) || got.IsValid() {
			t.Fatalf("parse(%v [%T]) = %v, %v; want invalid and parse identity", input, input, got, err)
		}
	}
}
