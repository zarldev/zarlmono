package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const manualInvocation = "12345678-1234-4234-9234-123456789abc"

func manualFixture() engine.RecordedSessionManualAccounting {
	r := recordedAccountingFixture()
	return engine.RecordedSessionManualAccounting{
		SessionID: "session/opaque", InvocationID: manualInvocation,
		Purpose: engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, StreamOutcome: inference.StreamOutcomes.COMPLETED,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

func encodeManual(t *testing.T, r engine.RecordedSessionManualAccounting) []byte {
	t.Helper()
	b, err := engine.EncodeRecordedSessionManualAccounting(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func decodeManual(t *testing.T, b []byte) engine.RecordedSessionManualAccounting {
	t.Helper()
	r, err := engine.DecodeRecordedSessionManualAccounting(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSessionManualCodecEvidenceAndOwnership(t *testing.T) {
	r := manualFixture()
	r.ConfiguredEstimate.Subtotal = 77
	r.ReportedCost.Source = "PRIVATE-CANARY"
	original := r.Clone()
	b := encodeManual(t, r)
	if bytes.Contains(b, []byte("PRIVATE-CANARY")) {
		t.Fatal("cost Source persisted")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 13 || fields["session_id"] == nil || fields["owner_request_batch_id"] != nil || fields["execution"] != nil {
		t.Fatalf("wire shape: %s", b)
	}
	r.Usage.PromptTokens++
	r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens++
	r.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	r.ConfiguredEstimate.Issues[0].Field = "changed"
	if original.ConfiguredEstimate.Issues[0].Field == "changed" || original.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens == r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens || original.ReportedCost.Source != "PRIVATE-CANARY" {
		t.Fatal("Clone retained aliases or lost domain Source")
	}
	got := decodeManual(t, b)
	original.ReportedCost.Source = ""
	if !reflect.DeepEqual(got, original) {
		t.Fatal("encoded evidence was mutated or repriced")
	}
	got.RateCard.CacheWrites[0].Rate.PerMillionTokens++
	clear(b)
	if !reflect.DeepEqual(decodeManual(t, encodeManual(t, original)), original) {
		t.Fatal("round trip changed evidence")
	}
	for mask := range 32 {
		v := engine.RecordedSessionManualAccounting{SessionID: "opaque", InvocationID: manualInvocation, Purpose: engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER, StreamOutcome: inference.StreamOutcomes.FAILED}
		if mask&1 != 0 {
			v.Usage = &llm.Usage{}
		}
		if mask&2 != 0 {
			v.PricingUsage = &llm.PricingUsage{}
		}
		if mask&4 != 0 {
			v.RateCard = &llm.RateCard{}
		}
		if mask&8 != 0 {
			v.ConfiguredEstimate = &llm.PriceEstimate{}
		}
		if mask&16 != 0 {
			v.ReportedCost = &llm.ReportedCost{}
		}
		if got := decodeManual(t, encodeManual(t, v)); !reflect.DeepEqual(got, v) {
			t.Fatalf("presence changed: %d", mask)
		}
	}
}

func TestSessionManualCodecVocabulary(t *testing.T) {
	for _, purpose := range []engine.AuxiliaryAccountingPurpose{engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, engine.AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER} {
		for outcome := range inference.StreamOutcomes.All() {
			r := manualFixture()
			r.Purpose = purpose
			r.StreamOutcome = outcome
			if got := decodeManual(t, encodeManual(t, r)); !reflect.DeepEqual(got, r) {
				t.Fatal("vocabulary changed")
			}
		}
	}
	for _, purpose := range []engine.AuxiliaryAccountingPurpose{engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE, engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER, engine.AuxiliaryAccountingPurposes.PURPOSENONE} {
		r := manualFixture()
		r.Purpose = purpose
		if _, err := engine.EncodeRecordedSessionManualAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedSessionManualAccounting) {
			t.Fatalf("purpose %v: %v", purpose, err)
		}
	}
}

func TestSessionManualCodecStrictBoundary(t *testing.T) {
	b := encodeManual(t, manualFixture())
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"version", bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1), engine.ErrRecordedSessionManualAccountingVersion},
		{"version fraction", bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1.0`), 1), nil},
		{"missing", bytes.Replace(b, []byte(`"session_id":"session/opaque",`), nil, 1), nil},
		{"null", bytes.Replace(b, []byte(`"session_id":"session/opaque"`), []byte(`"session_id":null`), 1), nil},
		{"empty", bytes.Replace(b, []byte(`"session_id":"session/opaque"`), []byte(`"session_id":""`), 1), nil},
		{"invalid unicode", bytes.Replace(b, []byte(`"session_id":"session/opaque"`), []byte(`"session_id":"\ud800"`), 1), nil},
		{"invalid UTF8", append(bytes.Clone(b), 0xff), nil},
		{"duplicate escaped", append([]byte(`{"\u0076ersion":1,`), b[1:]...), nil},
		{"unknown", append([]byte(`{"secret":"PRIVATE-CANARY",`), b[1:]...), nil},
		{"wrong case", bytes.Replace(b, []byte(`"stream_outcome"`), []byte(`"Stream_Outcome"`), 1), nil},
		{"uuid uppercase", bytes.Replace(b, []byte(manualInvocation), []byte(strings.ToUpper(manualInvocation)), 1), nil},
		{"uuid v1", bytes.Replace(b, []byte(manualInvocation), []byte("12345678-1234-1234-9234-123456789abc"), 1), nil},
		{"judge", bytes.Replace(b, []byte(`"purpose":"summary"`), []byte(`"purpose":"judge"`), 1), nil},
		{"unknown outcome", bytes.Replace(b, []byte(`"stream_outcome":"completed"`), []byte(`"stream_outcome":"other"`), 1), nil},
		{"number absence", bytes.Replace(b, []byte(`"usage":`), []byte(`"usage":{"prompt_tokens":-1},"ignored":`), 1), nil},
		{"too large", bytes.Repeat([]byte(" "), engine.MaxRecordedSessionManualAccountingBytes+1), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == nil {
				want = engine.ErrInvalidRecordedSessionManualAccounting
			}
			got, err := engine.DecodeRecordedSessionManualAccounting(tc.data)
			if !errors.Is(err, want) || err.Error() != want.Error() || errors.Unwrap(err) != nil || got.SessionID != "" || got.Usage != nil {
				t.Fatalf("got %+v, %v; want %v", got, err, want)
			}
		})
	}
}
