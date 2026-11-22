package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func assertInvalidHeadlessAccounting(t *testing.T, data []byte, want error) {
	t.Helper()
	got, err := engine.DecodeRecordedHeadlessProviderAccounting(data)
	if !errors.Is(err, want) {
		t.Fatalf("Decode error = %v; want %v", err, want)
	}
	// These errors deliberately expose no parser cause, field/input text, or
	// partially decoded accounting evidence on failure.
	if err.Error() != want.Error() || errors.Unwrap(err) != nil {
		t.Fatalf("unsafe diagnostic chain: %v", err)
	}
	if got.RunID != "" || got.PursueAttempt != 0 || got.ProviderOrdinal != 0 || got.Usage != nil || got.RateCard != nil ||
		got.PricingUsage != nil || got.ConfiguredEstimate != nil || got.ReportedCost != nil || got.Provider != "" || got.Model != "" {
		t.Fatal("invalid storage returned partial evidence")
	}
}

func TestRecordedHeadlessProviderAccountingStrictObjects(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAccounting(t, recordedHeadlessAccountingFixture())
	for _, object := range []struct {
		path     string
		optional []string
	}{
		{path: "", optional: []string{"usage", "pricing_usage", "rate_card", "configured_estimate", "reported_cost"}},
		{path: "usage", optional: []string{"reasoning_tokens"}},
		{path: "pricing_usage"},
		{path: "pricing_usage.input", optional: []string{"total", "cache_read", "cache_write", "other", "writes_by_lifetime"}},
		{path: "pricing_usage.output", optional: []string{"total", "other"}},
		{path: "pricing_usage.input.writes_by_lifetime.0", optional: []string{"quantity"}},
		{path: "rate_card", optional: []string{"fresh_input_per_million", "cache_read_per_million", "output_per_million", "cache_writes"}},
		{path: "rate_card.reference"}, {path: "rate_card.provenance"},
		{path: "rate_card.cache_writes.0", optional: []string{"per_million"}},
		{path: "configured_estimate", optional: []string{"issues"}},
		{path: "configured_estimate.reference"}, {path: "configured_estimate.provenance"},
		{path: "configured_estimate.issues.0"}, {path: "reported_cost"},
	} {
		t.Run(object.path, func(t *testing.T) {
			t.Parallel()
			var path []string
			if object.path != "" {
				path = strings.Split(object.path, ".")
			}
			var fields map[string]json.RawMessage
			editAccountingJSON(t, data, path, func(raw []byte) []byte {
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				return raw
			})
			unknown := editAccountingJSON(t, data, path, func(raw []byte) []byte {
				return append(append([]byte(`{"PRIVATE-CANARY-UNKNOWN":null,`), raw[1:]...), '\n')
			})
			assertInvalidHeadlessAccounting(t, unknown, engine.ErrInvalidRecordedHeadlessProviderAccounting)
			for key, value := range fields {
				encodedKey, err := json.Marshal(key)
				if err != nil {
					t.Fatal(err)
				}
				duplicate := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					out := append([]byte{'{'}, encodedKey...)
					out = append(out, ':')
					out = append(out, value...)
					out = append(out, ',')
					return append(out, raw[1:]...)
				})
				assertInvalidHeadlessAccounting(t, duplicate, engine.ErrInvalidRecordedHeadlessProviderAccounting)

				wrongCase := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					return bytes.Replace(raw, encodedKey, []byte(strconv.Quote(strings.ToUpper(key))), 1)
				})
				assertInvalidHeadlessAccounting(t, wrongCase, engine.ErrInvalidRecordedHeadlessProviderAccounting)
				escapedDuplicate := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					escaped := `"\u` + fmt.Sprintf("%04x", key[0]) + key[1:] + `":`
					return append(append(append([]byte("{"+escaped), value...), ','), raw[1:]...)
				})
				assertInvalidHeadlessAccounting(t, escapedDuplicate, engine.ErrInvalidRecordedHeadlessProviderAccounting)
				optional := false
				for _, candidate := range object.optional {
					optional = optional || key == candidate
				}
				missing := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					var members map[string]json.RawMessage
					if err := json.Unmarshal(raw, &members); err != nil {
						t.Fatal(err)
					}
					delete(members, key)
					out, err := json.Marshal(members)
					if err != nil {
						t.Fatal(err)
					}
					return out
				})
				fullPath := append(append([]string(nil), path...), key)
				null := editAccountingJSON(t, data, fullPath, func([]byte) []byte { return []byte("null") })
				if optional {
					if object.path == "" && key == "configured_estimate" {
						// Without its frozen estimate, "available" is inconsistent.
						assertInvalidHeadlessAccounting(t, missing, engine.ErrInvalidRecordedHeadlessProviderAccounting)
						assertInvalidHeadlessAccounting(t, null, engine.ErrInvalidRecordedHeadlessProviderAccounting)
					} else {
						decodeRecordedHeadlessAccounting(t, missing)
						decodeRecordedHeadlessAccounting(t, null)
					}
				} else {
					assertInvalidHeadlessAccounting(t, missing, engine.ErrInvalidRecordedHeadlessProviderAccounting)
					assertInvalidHeadlessAccounting(t, null, engine.ErrInvalidRecordedHeadlessProviderAccounting)
				}
			}
		})
	}
}

func TestRecordedHeadlessProviderAccountingRejectsStoredScalars(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAccounting(t, recordedHeadlessAccountingFixture())
	for _, tc := range []struct {
		path, value string
		want        error
	}{
		{path: "version", value: "2", want: engine.ErrRecordedHeadlessProviderAccountingVersion},
		{path: "version", value: "0", want: engine.ErrRecordedHeadlessProviderAccountingVersion},
		{path: "version", value: "-1", want: engine.ErrRecordedHeadlessProviderAccountingVersion},
		{path: "version", value: `"1"`}, {path: "version", value: "1.0"}, {path: "version", value: "1e0"},
		{path: "pursue_attempt", value: "0"}, {path: "pursue_attempt", value: "-1"}, {path: "pursue_attempt", value: "1.5"},
		{path: "pursue_attempt", value: "9223372036854775808"},
		{path: "run_id", value: `""`}, {path: "provider_ordinal", value: "0"}, {path: "provider_ordinal", value: "-1"}, {path: "provider_ordinal", value: "1.5"}, {path: "provider_ordinal", value: "9223372036854775808"}, {path: "provider", value: "1"},
		{path: "model", value: "{}"}, {path: "usage", value: "[]"},
		{path: "estimate_availability", value: `"invalid"`}, {path: "estimate_availability", value: `"AVAILABLE"`},
		{path: "estimate_availability", value: `"missing_card"`}, {path: "estimate_availability", value: `"PRIVATE-CANARY"`},
		{path: "usage.prompt_tokens", value: "-1"}, {path: "usage.completion_tokens", value: "-1"},
		{path: "usage.total_tokens", value: "-1"}, {path: "usage.cached_tokens", value: "-1"},
		{path: "usage.cache_creation_tokens", value: "-1"}, {path: "usage.cache_creation_5m_tokens", value: "-1"},
		{path: "usage.cache_creation_1h_tokens", value: "-1"}, {path: "usage.reasoning_tokens", value: "-1"},
		{path: "pricing_usage.input.total", value: "-1"}, {path: "pricing_usage.input.cache_read", value: "-1"},
		{path: "pricing_usage.input.cache_write", value: "-1"}, {path: "pricing_usage.input.other", value: "-1"},
		{path: "pricing_usage.output.total", value: "-1"}, {path: "pricing_usage.output.other", value: "-1"},
		{path: "pricing_usage.input.total", value: "9223372036854775808"},
		{path: "pricing_usage.input.total", value: "1e-9999"}, {path: "pricing_usage.input.total", value: "1.0"},
		{path: "pricing_usage.input.partition_known", value: "0"},
		{path: "pricing_usage.additional_charges_possible", value: `"true"`},
		{path: "pricing_usage.input.writes_by_lifetime.0.lifetime_ns", value: "-1"},
		{path: "pricing_usage.input.writes_by_lifetime.0.quantity", value: "-1"},
		{path: "pricing_usage.input.writes_by_lifetime.0", value: "null"},
		{path: "rate_card.fresh_input_per_million", value: "-1"}, {path: "rate_card.cache_read_per_million", value: "-1"},
		{path: "rate_card.output_per_million", value: "-1"},
		{path: "rate_card.cache_writes.0.lifetime_ns", value: "-1"}, {path: "rate_card.cache_writes.0.per_million", value: "-1"},
		{path: "rate_card.cache_writes.0", value: "null"},
		{path: "rate_card.billing", value: `"TOKEN-METERED"`}, {path: "rate_card.billing", value: `"PRIVATE-CANARY"`},
		{path: "rate_card.provenance.source", value: `"Configuration"`},
		{path: "rate_card.provenance.observed_at", value: `"PRIVATE-CANARY"`},
		{path: "rate_card.provenance.expires_at", value: "1"},
		{path: "configured_estimate.input_amount", value: "-1"}, {path: "configured_estimate.output_amount", value: "-1"},
		{path: "configured_estimate.subtotal", value: "-1"}, {path: "configured_estimate.complete", value: "1"},
		{path: "configured_estimate.billing", value: `"PRIVATE-CANARY"`},
		{path: "configured_estimate.provenance.source", value: `"PRIVATE-CANARY"`},
		{path: "configured_estimate.issues.0.kind", value: `"PRIVATE-CANARY"`},
		{path: "configured_estimate.issues.0.lifetime_ns", value: "-1"}, {path: "configured_estimate.issues.0", value: "null"},
		{path: "reported_cost.amount", value: "-1"}, {path: "reported_cost.currency", value: "1"},
		{path: "reported_cost.reported", value: `"true"`},
	} {
		t.Run(tc.path+"="+tc.value, func(t *testing.T) {
			t.Parallel()
			want := tc.want
			if want == nil {
				want = engine.ErrInvalidRecordedHeadlessProviderAccounting
			}
			assertInvalidHeadlessAccounting(t, setAccountingJSON(t, data, tc.path, tc.value), want)
		})
	}
	for _, path := range []string{"rate_card.fresh_input_per_million", "rate_card.cache_writes.0.per_million",
		"configured_estimate.input_amount", "configured_estimate.output_amount", "configured_estimate.subtotal", "reported_cost.amount"} {
		for _, number := range []string{"1e9999", "1e-9999", "2e-324", "-1e-9999", `"NaN"`} {
			t.Run(path+"="+number, func(t *testing.T) {
				t.Parallel()
				assertInvalidHeadlessAccounting(t, setAccountingJSON(t, data, path, number), engine.ErrInvalidRecordedHeadlessProviderAccounting)
			})
		}
	}
	// The smallest representable positive float must survive, not become free.
	for _, number := range []string{"5e-324", "0e9999", "0e-9999"} {
		wire := setAccountingJSON(t, data, "rate_card.fresh_input_per_million", number)
		got := decodeRecordedHeadlessAccounting(t, wire)
		want, err := strconv.ParseFloat(number, 64)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RateCard.FreshInput.Known || got.RateCard.FreshInput.PerMillionTokens != want {
			t.Fatal("representable rate changed")
		}
	}
}

func TestRecordedHeadlessProviderAccountingRejectsInvalidSnapshots(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedHeadlessProviderAccounting)
	}{
		{name: "empty receipt", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.RunID = "" }},
		{name: "zero provider ordinal", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.ProviderOrdinal = 0 }},
		{name: "nonpositive attempt", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.PursueAttempt = 0 }},
		{name: "negative quantity", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.PricingUsage.Input.Total.Tokens = -1 }},
		{name: "negative known legacy usage", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.Usage.ReasoningTokens = -1 }},
		{name: "negative write quantity", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = -1
		}},
		{name: "negative lifetime", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.RateCard.CacheWrites[0].Lifetime = -1 }},
		{name: "known rate NaN", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.RateCard.Output.PerMillionTokens = math.NaN() }},
		{name: "known rate infinity", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.RateCard.FreshInput.PerMillionTokens = math.Inf(1)
		}},
		{name: "known write rate negative", edit: func(r *engine.RecordedHeadlessProviderAccounting) {
			r.RateCard.CacheWrites[0].Rate.PerMillionTokens = -1
		}},
		{name: "known estimate infinity", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.ConfiguredEstimate.Subtotal = math.Inf(1) }},
		{name: "reported cost NaN", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.ReportedCost.Amount = math.NaN() }},
		{name: "reported cost negative", edit: func(r *engine.RecordedHeadlessProviderAccounting) { r.ReportedCost.Amount = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := recordedHeadlessAccountingFixture()
			tc.edit(&r)
			data, err := engine.EncodeRecordedHeadlessProviderAccounting(r)
			if !errors.Is(err, engine.ErrInvalidRecordedHeadlessProviderAccounting) || data != nil {
				t.Fatalf("Encode returned %d bytes and %v", len(data), err)
			}
		})
	}
}

func TestRecordedHeadlessProviderAccountingEncodingAndBounds(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAccounting(t, recordedHeadlessAccountingFixture())
	for _, bad := range [][]byte{nil, {}, []byte("null"), []byte("[]"), []byte("1"), []byte(`"PRIVATE-CANARY"`),
		[]byte("{"), append(bytes.Clone(data), []byte(" {}")...), append(bytes.Clone(data), []byte(" PRIVATE-CANARY")...),
		bytes.Replace(data, []byte(`"run_id":`), []byte(`"run_id":"PRIVATE-CANARY","run_id":`), 1),
		setAccountingJSON(t, data, "run_id", `"\ud800"`), setAccountingJSON(t, data, "run_id", `"\udfff"`),
		setAccountingJSON(t, data, "model", `"\ud800\u0041"`),
	} {
		assertInvalidHeadlessAccounting(t, bad, engine.ErrInvalidRecordedHeadlessProviderAccounting)
	}
	invalidUTF8 := bytes.Replace(data, []byte("Mixed"), []byte{0xff}, 1)
	assertInvalidHeadlessAccounting(t, invalidUTF8, engine.ErrInvalidRecordedHeadlessProviderAccounting)
	valid := setAccountingJSON(t, data, "run_id", `" Exact / Case \ud83d\ude00 \ufffd "`)
	if got := decodeRecordedHeadlessAccounting(t, valid); got.RunID != " Exact / Case 😀 � " {
		t.Fatal("valid UTF-8 identity was repaired or normalized")
	}
	for _, edit := range []func(*engine.RecordedHeadlessProviderAccounting){
		func(r *engine.RecordedHeadlessProviderAccounting) { r.RunID = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessProviderAccounting) { r.Provider = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessProviderAccounting) { r.RateCard.Reference.ID = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessProviderAccounting) {
			r.RateCard.Provenance.Snapshot = string([]byte{0xff})
		},
		func(r *engine.RecordedHeadlessProviderAccounting) {
			r.ConfiguredEstimate.Issues[0].Field = string([]byte{0xff})
		},
		func(r *engine.RecordedHeadlessProviderAccounting) {
			r.ReportedCost.Currency = llm.Currency(string([]byte{0xff}))
		},
	} {
		r := recordedHeadlessAccountingFixture()
		edit(&r)
		if _, err := engine.EncodeRecordedHeadlessProviderAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessProviderAccounting) {
			t.Fatalf("invalid UTF-8 encoded with %v", err)
		}
	}
	// Bound the complete encoded object, not just a single field. Plain ASCII
	// makes the exact 1 MiB boundary independent of JSON escaping expansion.
	r := engine.RecordedHeadlessProviderAccounting{RunID: " ", PursueAttempt: 1, ProviderOrdinal: 1}
	base := encodeRecordedHeadlessAccounting(t, r)
	r.Model = strings.Repeat("x", engine.MaxRecordedHeadlessProviderAccountingBytes-len(base))
	exact := encodeRecordedHeadlessAccounting(t, r)
	if len(exact) != engine.MaxRecordedHeadlessProviderAccountingBytes {
		t.Fatal("test did not reach the exact byte limit")
	}
	if got := decodeRecordedHeadlessAccounting(t, exact); got.Model != r.Model || got.RunID != " " || got.ProviderOrdinal != 1 {
		t.Fatal("valid opaque identity or limit-sized label changed")
	}
	assertInvalidHeadlessAccounting(t, append(bytes.Clone(exact), ' '), engine.ErrInvalidRecordedHeadlessProviderAccounting)
	r.Model += "x"
	if data, err := engine.EncodeRecordedHeadlessProviderAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessProviderAccounting) || data != nil {
		t.Fatal("oversized output encoded")
	}
}
