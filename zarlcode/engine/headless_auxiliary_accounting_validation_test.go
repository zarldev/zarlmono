package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func assertInvalidHeadlessAuxiliaryAccounting(t *testing.T, data []byte, want error) {
	t.Helper()
	got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(data)
	if !errors.Is(err, want) {
		t.Fatalf("Decode error = %v; want %v", err, want)
	}
	// These errors deliberately expose no parser cause, field/input text, or
	// partially decoded accounting evidence on failure.
	if err.Error() != want.Error() || errors.Unwrap(err) != nil {
		t.Fatalf("unsafe diagnostic chain: %v", err)
	}
	if got.RunID != "" || got.InvocationID != "" || got.Usage != nil || got.RateCard != nil ||
		got.PricingUsage != nil || got.ConfiguredEstimate != nil || got.ReportedCost != nil || got.Provider != "" || got.Model != "" {
		t.Fatal("invalid storage returned partial evidence")
	}
}

func TestRecordedHeadlessAuxiliaryAccountingStrictObjects(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAuxiliaryAccounting(t, recordedHeadlessAuxiliaryAccountingFixture())
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
			assertInvalidHeadlessAuxiliaryAccounting(t, unknown, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
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
				assertInvalidHeadlessAuxiliaryAccounting(t, duplicate, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
				// Escaped member names collide after decoding at every nesting level.
				escaped := []byte(`"\u00` + strconv.FormatInt(int64(key[0]), 16) + key[1:] + `"`)
				escapedDuplicate := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					out := append([]byte{'{'}, escaped...)
					out = append(out, ':')
					out = append(out, value...)
					out = append(out, ',')
					return append(out, raw[1:]...)
				})
				assertInvalidHeadlessAuxiliaryAccounting(t, escapedDuplicate, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
				wrongCase := editAccountingJSON(t, data, path, func(raw []byte) []byte {
					return bytes.Replace(raw, encodedKey, []byte(strconv.Quote(strings.ToUpper(key))), 1)
				})
				assertInvalidHeadlessAuxiliaryAccounting(t, wrongCase, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
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
						assertInvalidHeadlessAuxiliaryAccounting(t, missing, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
						assertInvalidHeadlessAuxiliaryAccounting(t, null, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
					} else {
						decodeRecordedHeadlessAuxiliaryAccounting(t, missing)
						decodeRecordedHeadlessAuxiliaryAccounting(t, null)
					}
				} else {
					assertInvalidHeadlessAuxiliaryAccounting(t, missing, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
					assertInvalidHeadlessAuxiliaryAccounting(t, null, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
				}
			}
		})
	}
}

func TestRecordedHeadlessAuxiliaryAccountingRejectsStoredScalars(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAuxiliaryAccounting(t, recordedHeadlessAuxiliaryAccountingFixture())
	for _, tc := range []struct {
		path, value string
		want        error
	}{
		{path: "version", value: "2", want: engine.ErrRecordedHeadlessAuxiliaryAccountingVersion},
		{path: "version", value: "0", want: engine.ErrRecordedHeadlessAuxiliaryAccountingVersion},
		{path: "version", value: "-1", want: engine.ErrRecordedHeadlessAuxiliaryAccountingVersion},
		{path: "version", value: `"1"`}, {path: "version", value: "1.0"}, {path: "version", value: "1e0"},
		{path: "run_id", value: `""`}, {path: "invocation_id", value: `""`}, {path: "provider", value: "1"},
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
				want = engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting
			}
			assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, tc.path, tc.value), want)
		})
	}
	for _, path := range []string{"rate_card.fresh_input_per_million", "rate_card.cache_writes.0.per_million",
		"configured_estimate.input_amount", "configured_estimate.output_amount", "configured_estimate.subtotal", "reported_cost.amount"} {
		for _, number := range []string{"1e9999", "1e-9999", "2e-324", "-1e-9999", `"NaN"`} {
			t.Run(path+"="+number, func(t *testing.T) {
				t.Parallel()
				assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, path, number), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
			})
		}
	}
	// The smallest representable positive float must survive, not become free.
	for _, number := range []string{"5e-324", "0e9999", "0e-9999"} {
		wire := setAccountingJSON(t, data, "rate_card.fresh_input_per_million", number)
		got := decodeRecordedHeadlessAuxiliaryAccounting(t, wire)
		want, err := strconv.ParseFloat(number, 64)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RateCard.FreshInput.Known || got.RateCard.FreshInput.PerMillionTokens != want {
			t.Fatal("representable rate changed")
		}
	}
}

func TestRecordedHeadlessAuxiliaryAccountingRejectsInvalidSnapshots(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedHeadlessAuxiliaryAccounting)
	}{
		{name: "empty run", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.RunID = "" }},
		{name: "empty invocation", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.InvocationID = "" }},
		{name: "negative quantity", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.PricingUsage.Input.Total.Tokens = -1 }},
		{name: "negative known legacy usage", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.Usage.ReasoningTokens = -1 }},
		{name: "negative write quantity", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = -1
		}},
		{name: "negative lifetime", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.RateCard.CacheWrites[0].Lifetime = -1 }},
		{name: "known rate NaN", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.RateCard.Output.PerMillionTokens = math.NaN() }},
		{name: "known rate infinity", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.RateCard.FreshInput.PerMillionTokens = math.Inf(1)
		}},
		{name: "known write rate negative", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.RateCard.CacheWrites[0].Rate.PerMillionTokens = -1
		}},
		{name: "known estimate infinity", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.ConfiguredEstimate.Subtotal = math.Inf(1) }},
		{name: "reported cost NaN", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.ReportedCost.Amount = math.NaN() }},
		{name: "reported cost negative", edit: func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.ReportedCost.Amount = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := recordedHeadlessAuxiliaryAccountingFixture()
			tc.edit(&r)
			data, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r)
			if !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) || data != nil {
				t.Fatalf("Encode returned %d bytes and %v", len(data), err)
			}
		})
	}
}

func TestRecordedHeadlessAuxiliaryAccountingEncodingAndBounds(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAuxiliaryAccounting(t, recordedHeadlessAuxiliaryAccountingFixture())
	for _, bad := range [][]byte{nil, {}, []byte("null"), []byte("[]"), []byte("1"), []byte(`"PRIVATE-CANARY"`),
		[]byte("{"), append(bytes.Clone(data), []byte(" {}")...), append(bytes.Clone(data), []byte(" PRIVATE-CANARY")...),
		bytes.Replace(data, []byte(`"invocation_id":`), []byte(`"invocation_id":"PRIVATE-CANARY","invocation_id":`), 1),
		setAccountingJSON(t, data, "invocation_id", `"\ud800"`), setAccountingJSON(t, data, "invocation_id", `"\udfff"`),
		setAccountingJSON(t, data, "model", `"\ud800\u0041"`),
	} {
		assertInvalidHeadlessAuxiliaryAccounting(t, bad, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
	}
	invalidUTF8 := bytes.Replace(data, []byte("Mixed"), []byte{0xff}, 1)
	assertInvalidHeadlessAuxiliaryAccounting(t, invalidUTF8, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
	valid := setAccountingJSON(t, data, "run_id", `" Exact / Case \ud83d\ude00 \ufffd "`)
	if got := decodeRecordedHeadlessAuxiliaryAccounting(t, valid); got.RunID != " Exact / Case 😀 � " {
		t.Fatal("valid UTF-8 identity was repaired or normalized")
	}
	for _, edit := range []func(*engine.RecordedHeadlessAuxiliaryAccounting){
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.RunID = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.InvocationID = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.Provider = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) { r.RateCard.Reference.ID = string([]byte{0xff}) },
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.RateCard.Provenance.Snapshot = string([]byte{0xff})
		},
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.ConfiguredEstimate.Issues[0].Field = string([]byte{0xff})
		},
		func(r *engine.RecordedHeadlessAuxiliaryAccounting) {
			r.ReportedCost.Currency = llm.Currency(string([]byte{0xff}))
		},
	} {
		r := recordedHeadlessAuxiliaryAccountingFixture()
		edit(&r)
		if _, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) {
			t.Fatalf("invalid UTF-8 encoded with %v", err)
		}
	}
	// Bound the complete encoded object, not just a single field. Plain ASCII
	// makes the exact 1 MiB boundary independent of JSON escaping expansion.
	r := engine.RecordedHeadlessAuxiliaryAccounting{RunID: " ", InvocationID: headlessAuxiliaryInvocationUUID, Purpose: engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE, StreamOutcome: inference.StreamOutcomes.COMPLETED}
	base := encodeRecordedHeadlessAuxiliaryAccounting(t, r)
	r.Model = strings.Repeat("x", engine.MaxRecordedHeadlessAuxiliaryAccountingBytes-len(base))
	exact := encodeRecordedHeadlessAuxiliaryAccounting(t, r)
	if len(exact) != engine.MaxRecordedHeadlessAuxiliaryAccountingBytes {
		t.Fatal("test did not reach the exact byte limit")
	}
	if got := decodeRecordedHeadlessAuxiliaryAccounting(t, exact); got.Model != r.Model || got.RunID != " " || got.InvocationID != headlessAuxiliaryInvocationUUID {
		t.Fatal("valid opaque identity or limit-sized label changed")
	}
	assertInvalidHeadlessAuxiliaryAccounting(t, append(bytes.Clone(exact), ' '), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
	r.Model += "x"
	if data, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) || data != nil {
		t.Fatal("oversized output encoded")
	}
}

func TestRecordedHeadlessAuxiliaryAccountingUUIDAndVocabulary(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAuxiliaryAccounting(t, recordedHeadlessAuxiliaryAccountingFixture())
	for _, id := range []string{
		"", "00000000-0000-0000-0000-000000000000", "12345678-1234-1234-9234-123456789abc",
		"12345678-1234-4234-0234-123456789abc", "12345678-1234-4234-d234-123456789abc",
		"12345678-1234-4234-9234-123456789ABC", "12345678123442349234123456789abc",
		"{12345678-1234-4234-9234-123456789abc}", "urn:uuid:" + headlessAuxiliaryInvocationUUID,
		" " + headlessAuxiliaryInvocationUUID, headlessAuxiliaryInvocationUUID + " ", "PRIVATE-CANARY",
	} {
		quoted, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, "invocation_id", string(quoted)), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
		r := recordedHeadlessAuxiliaryAccountingFixture()
		r.InvocationID = id
		if wire, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) || wire != nil {
			t.Fatal("encoded noncanonical UUID", err)
		}
	}
	for _, field := range []string{"purpose", "stream_outcome"} {
		for _, value := range []string{`"none"`, `""`, `"PRIVATE-CANARY"`, `1`, `"1"`, `"COMPLETED"`, `"JUDGE"`, `" summary "`, `false`, `{}`} {
			assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, field, value), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
		}
	}
	r := recordedHeadlessAuxiliaryAccountingFixture()
	r.Purpose = engine.AuxiliaryAccountingPurpose{}
	if wire, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) || wire != nil {
		t.Fatal("zero purpose", err)
	}
	r = recordedHeadlessAuxiliaryAccountingFixture()
	r.StreamOutcome = inference.StreamOutcome{}
	if wire, err := engine.EncodeRecordedHeadlessAuxiliaryAccounting(r); !errors.Is(err, engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting) || wire != nil {
		t.Fatal("zero outcome", err)
	}
}

// Traverse the explicit JSON fixture, not Go implementation fields, so every
// string/number in the actual persisted schema gets Unicode/numeric adversaries.
func headlessAuxiliaryScalarPaths(t *testing.T, raw []byte, path string, visit func(string, json.RawMessage)) {
	t.Helper()
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		t.Fatal("empty fixture member")
	}
	switch raw[0] {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		for key, value := range object {
			next := key
			if path != "" {
				next = path + "." + key
			}
			headlessAuxiliaryScalarPaths(t, value, next, visit)
		}
	case '[':
		var array []json.RawMessage
		if err := json.Unmarshal(raw, &array); err != nil {
			t.Fatal(err)
		}
		for i, value := range array {
			headlessAuxiliaryScalarPaths(t, value, path+"."+strconv.Itoa(i), visit)
		}
	default:
		visit(path, raw)
	}
}

func TestRecordedHeadlessAuxiliaryAccountingAllScalarBoundaries(t *testing.T) {
	t.Parallel()
	data := encodeRecordedHeadlessAuxiliaryAccounting(t, recordedHeadlessAuxiliaryAccountingFixture())
	headlessAuxiliaryScalarPaths(t, data, "", func(path string, raw json.RawMessage) {
		if raw[0] == '"' {
			for _, value := range []string{`"\ud800"`, `"\udfff"`, `"\ud800\u0041"`, "\"\xff\""} {
				assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, path, value), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
			}
		} else if raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9') {
			for _, value := range []string{"9223372036854775808e9999", "1e-9999", "2e-324", "-1e-9999", `"NaN"`, `"Infinity"`} {
				assertInvalidHeadlessAuxiliaryAccounting(t, setAccountingJSON(t, data, path, value), engine.ErrInvalidRecordedHeadlessAuxiliaryAccounting)
			}
		}
	})
}
