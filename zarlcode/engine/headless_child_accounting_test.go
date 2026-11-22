package engine_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const headlessChildID = "b0b21caf-ff1c-4d34-90ad-48ee71fb4889"

func headlessChildFixture() engine.RecordedHeadlessChildProviderAccounting {
	r := recordedHeadlessAccountingFixture()
	return engine.RecordedHeadlessChildProviderAccounting{RunID: "opaque run", ChildTaskID: headlessChildID, Attempt: 2,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost}
}

func encodeHeadlessChild(t *testing.T, r engine.RecordedHeadlessChildProviderAccounting) []byte {
	t.Helper()
	b, err := engine.EncodeRecordedHeadlessChildProviderAccounting(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecordedHeadlessChildAccountingRoundTripOwnership(t *testing.T) {
	t.Parallel()
	r := headlessChildFixture()
	r.ReportedCost.Source = "private-source-canary"
	original := r.Clone()
	cloned := r.Clone()
	cloned.RateCard.CacheWrites[0].Lifetime++
	cloned.ConfiguredEstimate.Issues[0].Lifetime++
	cloned.ReportedCost.Source = "changed"
	if !reflect.DeepEqual(r, original) {
		t.Fatal("clone aliases original")
	}
	data := encodeHeadlessChild(t, r)
	if bytes.Contains(data, []byte(r.ReportedCost.Source)) {
		t.Fatal("private source stored")
	}
	got, err := engine.DecodeRecordedHeadlessChildProviderAccounting(data)
	if err != nil {
		t.Fatal(err)
	}
	want := r.Clone()
	want.ReportedCost.Source = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frozen evidence changed: got %+v want %+v", got, want)
	}
	data[0] = 'x'
	if !reflect.DeepEqual(got, want) {
		t.Fatal("decoder aliases wire")
	}
	for mask := range 32 {
		x := engine.RecordedHeadlessChildProviderAccounting{RunID: "r", ChildTaskID: headlessChildID, Attempt: 1}
		if mask&1 != 0 {
			x.Usage = &llm.Usage{}
		}
		if mask&2 != 0 {
			x.PricingUsage = &llm.PricingUsage{}
		}
		if mask&4 != 0 {
			x.RateCard = &llm.RateCard{}
		}
		if mask&8 != 0 {
			x.ConfiguredEstimate = &llm.PriceEstimate{}
		}
		if mask&16 != 0 {
			x.ReportedCost = &llm.ReportedCost{}
		}
		y, err := engine.DecodeRecordedHeadlessChildProviderAccounting(encodeHeadlessChild(t, x))
		if err != nil || !reflect.DeepEqual(y, x) {
			t.Fatalf("presence mask %d: %v got %+v", mask, err, y)
		}
	}
}

func TestRecordedHeadlessChildAccountingStrictWire(t *testing.T) {
	t.Parallel()
	valid := string(encodeHeadlessChild(t, engine.RecordedHeadlessChildProviderAccounting{RunID: "run", ChildTaskID: headlessChildID, Attempt: 1}))
	const want = `{"version":1,"run_id":"run","child_task_id":"b0b21caf-ff1c-4d34-90ad-48ee71fb4889","attempt":1,"provider":"","model":"","usage":null,"pricing_usage":null,"rate_card":null,"estimate_availability":"both_missing","configured_estimate":null,"reported_cost":null}`
	if valid != want {
		t.Fatalf("wire changed: %s", valid)
	}
	for _, tc := range []struct {
		name, wire string
		version    bool
	}{
		{"escaped duplicate", strings.Replace(valid, `"run_id":"run"`, `"run_id":"run","run_\u0069d":"run"`, 1), false},
		{"wrong case", strings.Replace(valid, `"run_id"`, `"Run_ID"`, 1), false},
		{"required null", strings.Replace(valid, `"attempt":1`, `"attempt":null`, 1), false},
		{"missing required", strings.Replace(valid, `"run_id":"run",`, "", 1), false},
		{"unknown", strings.Replace(valid, `"version":1`, `"version":1,"stream_outcome":"success"`, 1), false},
		{"nested unknown", strings.Replace(valid, `"usage":null`, `"usage":{"secret":1}`, 1), false},
		{"fractional attempt", strings.Replace(valid, `"attempt":1`, `"attempt":1.1`, 1), false},
		{"overflow", strings.Replace(valid, `"attempt":1`, `"attempt":9223372036854775808`, 1), false},
		{"version", strings.Replace(valid, `"version":1`, `"version":2`, 1), true},
		{"upper uuid", strings.Replace(valid, headlessChildID, strings.ToUpper(headlessChildID), 1), false},
		{"non v4", strings.Replace(valid, headlessChildID, "b0b21caf-ff1c-1d34-90ad-48ee71fb4889", 1), false},
		{"empty run", strings.Replace(valid, `"run_id":"run"`, `"run_id":""`, 1), false},
		{"unpaired surrogate", strings.Replace(valid, `"run_id":"run"`, `"run_id":"\ud800"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantErr := engine.ErrInvalidRecordedHeadlessChildProviderAccounting
			if tc.version {
				wantErr = engine.ErrRecordedHeadlessChildProviderAccountingVersion
			}
			if _, err := engine.DecodeRecordedHeadlessChildProviderAccounting([]byte(tc.wire)); !errors.Is(err, wantErr) {
				t.Fatalf("decode: %v want %v", err, wantErr)
			}
		})
	}
	for _, change := range []func(*engine.RecordedHeadlessChildProviderAccounting){
		func(r *engine.RecordedHeadlessChildProviderAccounting) { r.RunID = "" },
		func(r *engine.RecordedHeadlessChildProviderAccounting) { r.RunID = "\xff" },
		func(r *engine.RecordedHeadlessChildProviderAccounting) { r.ChildTaskID = "not-uuid" },
		func(r *engine.RecordedHeadlessChildProviderAccounting) { r.Attempt = 0 },
	} {
		r := headlessChildFixture()
		change(&r)
		if _, err := engine.EncodeRecordedHeadlessChildProviderAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessChildProviderAccounting) {
			t.Fatalf("encode: %v", err)
		}
	}
	if _, err := engine.DecodeRecordedHeadlessChildProviderAccounting(bytes.Repeat([]byte("x"), engine.MaxRecordedHeadlessChildProviderAccountingBytes+1)); !errors.Is(err, engine.ErrInvalidRecordedHeadlessChildProviderAccounting) {
		t.Fatal(err)
	}
	for _, b := range [][]byte{
		encodeRecordedHeadlessAccounting(t, engine.RecordedHeadlessProviderAccounting{RunID: "run", PursueAttempt: 1, ProviderOrdinal: 1}),
	} {
		if _, err := engine.DecodeRecordedHeadlessChildProviderAccounting(b); !errors.Is(err, engine.ErrInvalidRecordedHeadlessChildProviderAccounting) {
			t.Fatal(err)
		}
	}
}
