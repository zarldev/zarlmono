package modelsdev_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestEvidenceInvalidNumericAssertionsAreIndependentlyUnknown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		limits llm.ModelLimits
		prices modelsdev.PriceEvidence
	}{
		{"negative-input", `"limit":{"input":-1,"output":20,"context":100},"cost":{"input":-2,"output":3}`, llm.ModelLimits{OutputTokens: 20, ContextTokens: 100}, modelsdev.PriceEvidence{OutputKnown: true, OutputCostPerMTok: 3}},
		{"negative-output", `"limit":{"input":10,"output":-1,"context":100},"cost":{"input":2,"output":-3}`, llm.ModelLimits{InputTokens: 10, ContextTokens: 100}, modelsdev.PriceEvidence{InputKnown: true, InputCostPerMTok: 2}},
		{"negative-context", `"limit":{"input":10,"output":20,"context":-1},"cost":{"input":0,"output":0}`, llm.ModelLimits{InputTokens: 10, OutputTokens: 20}, modelsdev.PriceEvidence{InputKnown: true, OutputKnown: true}},
		{"zero", `"limit":{"input":0,"output":0,"context":0},"cost":{"input":0,"output":0}`, llm.ModelLimits{}, modelsdev.PriceEvidence{InputKnown: true, OutputKnown: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, store, _ := evidenceSource(t, `{"m":{"tool_call":false,`+tc.fields+`}}`)
			e, ok := source.LookupEvidence(t.Context(), "google", "m")
			if !ok || e.Limits != tc.limits || e.Pricing != tc.prices || e.Tools != llm.Supports.SUPPORTUNSUPPORTED {
				t.Fatalf("evidence = %+v, found=%v; want limits=%+v prices=%+v", e, ok, tc.limits, tc.prices)
			}
			entry, ok := source.LookupCached("gemini", "m")
			if !ok || entry.ContextWindow != int(tc.limits.ContextTokens) || entry.MaxOutputTokens != int(tc.limits.OutputTokens) || entry.InputCostPerMTok != tc.prices.InputCostPerMTok || entry.OutputCostPerMTok != tc.prices.OutputCostPerMTok {
				t.Fatalf("legacy projection = %+v, found=%v", entry, ok)
			}
			// Reuse the invalid wire numbers as persisted Evidence to exercise the
			// same validation at storage ingestion, independently of HTTP decoding.
			var persisted struct {
				Limit struct {
					Input   int64 `json:"input"`
					Output  int64 `json:"output"`
					Context int64 `json:"context"`
				} `json:"limit"`
				Cost struct {
					Input  float64 `json:"input"`
					Output float64 `json:"output"`
				} `json:"cost"`
			}
			if err := json.Unmarshal([]byte(`{`+tc.fields+`}`), &persisted); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
				Schema: 4, FetchedAt: time.Now(),
				Entries: map[string]map[string]modelsdev.Entry{"google": {"m": {Evidence: modelsdev.Evidence{
					Tools:   llm.Supports.SUPPORTUNSUPPORTED,
					Limits:  llm.ModelLimits{InputTokens: persisted.Limit.Input, OutputTokens: persisted.Limit.Output, ContextTokens: persisted.Limit.Context},
					Pricing: modelsdev.PriceEvidence{InputKnown: true, OutputKnown: true, InputCostPerMTok: persisted.Cost.Input, OutputCostPerMTok: persisted.Cost.Output},
				}}}},
			}); err != nil {
				t.Fatal(err)
			}
			loaded := modelsdev.New(store)
			restored, ok := loaded.LookupEvidence(t.Context(), "google", "m")
			if !ok || restored.Limits != e.Limits || restored.Pricing != e.Pricing || restored.Tools != e.Tools {
				t.Fatalf("storage ingestion = %+v, found=%v; want HTTP assertions %+v", restored, ok, e)
			}
		})
	}
}

func TestEvidenceMalformedNumbersFailFetchAndKeepLastGood(t *testing.T) {
	for _, fields := range []string{
		`"limit":{"input":"10"}`, `"limit":{"output":1.5}`, `"limit":{"context":1e100}`,
		`"cost":{"input":"2"}`, `"cost":{"output":"NaN"}`, `"cost":{"input":1e400}`,
		`"cost":{"output":NaN}`, `"limit":{"context":false}`, `"cost":{"output":{}}`,
	} {
		t.Run(fields, func(t *testing.T) {
			for _, lastGood := range []bool{false, true} {
				t.Run(map[bool]string{false: "cold", true: "last-good"}[lastGood], func(t *testing.T) {
					source, store, _ := evidenceSource(t, `{"m":{`+fields+`}}`)
					if lastGood {
						if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
							Schema: 4, FetchedAt: time.Now().Add(-24 * time.Hour),
							Entries: map[string]map[string]modelsdev.Entry{"google": {"m": {Evidence: modelsdev.Evidence{Tools: llm.Supports.SUPPORTUNSUPPORTED, Pricing: modelsdev.PriceEvidence{InputKnown: true}}}}},
						}); err != nil {
							t.Fatal(err)
						}
					}
					if err := source.Warm(t.Context()); err == nil {
						t.Fatal("malformed numeric representation did not fail the fetch")
					}
					e, ok := source.LookupEvidenceCached("google", "m")
					if ok != lastGood || (ok && (e.Tools != llm.Supports.SUPPORTUNSUPPORTED || !e.Pricing.InputKnown || e.Pricing.InputCostPerMTok != 0)) {
						t.Fatalf("last-good evidence = %+v, found=%v, want found=%v", e, ok, lastGood)
					}
					if _, ok := source.LookupCached("gemini", "m"); ok != lastGood {
						t.Fatalf("legacy last-good found=%v, want %v", ok, lastGood)
					}
				})
			}
		})
	}
}

func TestSnapshotEvidenceIsAuthoritativeForEveryLookup(t *testing.T) {
	yes, no := llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED
	for _, legacyFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "evidence-first", true: "legacy-first"}[legacyFirst], func(t *testing.T) {
			original := modelsdev.Snapshot{
				Schema: 4, FetchedAt: time.Now(),
				Entries: map[string]map[string]modelsdev.Entry{"google": {
					"false-zero": {
						Intrinsic:   modelsdev.Intrinsic{ContextWindow: 999, MaxOutputTokens: 999, SupportsTools: true, SupportsThinking: true, SupportsVision: true, SupportsVideo: true},
						Pricing:     modelsdev.Pricing{InputCostPerMTok: 999, OutputCostPerMTok: 999},
						Information: modelsdev.ModelInformation{Tools: yes, Reasoning: yes, Limits: llm.ModelLimits{InputTokens: -999, OutputTokens: 999, ContextTokens: -999}},
						Evidence:    modelsdev.Evidence{Tools: no, Reasoning: no, Limits: llm.ModelLimits{InputTokens: 10, OutputTokens: -20, ContextTokens: 100}, Pricing: modelsdev.PriceEvidence{InputKnown: true, OutputKnown: true, OutputCostPerMTok: -3}},
					},
					"supported": {Evidence: modelsdev.Evidence{Tools: yes, Reasoning: yes, Modalities: modelsdev.DirectionalModalities{Input: modelsdev.ModalityEvidence{Image: yes, Video: yes}}, Limits: llm.ModelLimits{OutputTokens: 20, ContextTokens: 200}, Pricing: modelsdev.PriceEvidence{InputCostPerMTok: 8, OutputKnown: true, OutputCostPerMTok: 3}}},
					"unknown":   {Intrinsic: modelsdev.Intrinsic{SupportsTools: true}, Pricing: modelsdev.Pricing{InputCostPerMTok: 999}, Information: modelsdev.ModelInformation{Tools: yes, Limits: llm.ModelLimits{InputTokens: 999}}},
				}},
			}
			encoded, err := json.Marshal(original) //nolint:musttag // Exercise the existing versioned Snapshot format.
			if err != nil {
				t.Fatal(err)
			}
			var restored modelsdev.Snapshot
			if err := json.Unmarshal(encoded, &restored); err != nil { //nolint:musttag // Exercise the existing versioned Snapshot format.
				t.Fatal(err)
			}
			store := &countingSnapshotCache{inner: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
			if err := store.Set(t.Context(), "models.dev", restored); err != nil {
				t.Fatal(err)
			}
			source := modelsdev.New(store)
			if legacyFirst {
				if _, ok := source.Lookup(t.Context(), "gemini", "false-zero"); !ok {
					t.Fatal("missing legacy entry")
				}
			} else if _, ok := source.LookupEvidence(t.Context(), "google", "false-zero"); !ok {
				t.Fatal("missing evidence")
			}
			for _, tc := range []struct {
				model     string
				intrinsic modelsdev.Intrinsic
				prices    modelsdev.PriceEvidence
				tools     llm.Support
			}{
				{"false-zero", modelsdev.Intrinsic{ContextWindow: 100}, modelsdev.PriceEvidence{InputKnown: true}, no},
				{"supported", modelsdev.Intrinsic{ContextWindow: 200, MaxOutputTokens: 20, SupportsTools: true, SupportsThinking: true, SupportsVision: true, SupportsVideo: true}, modelsdev.PriceEvidence{OutputKnown: true, OutputCostPerMTok: 3}, yes},
				{"unknown", modelsdev.Intrinsic{}, modelsdev.PriceEvidence{}, llm.Supports.SUPPORTUNKNOWN},
			} {
				entry, ok := source.Lookup(t.Context(), "google-vertex", tc.model)
				if !ok || entry.Intrinsic != tc.intrinsic || entry.Pricing != (modelsdev.Pricing{InputCostPerMTok: tc.prices.InputCostPerMTok, OutputCostPerMTok: tc.prices.OutputCostPerMTok}) || entry.Evidence.Pricing != tc.prices || entry.Evidence.Tools != tc.tools {
					t.Fatalf("%s entry = %+v, found=%v", tc.model, entry, ok)
				}
				cached, ok := source.LookupCached("gemini", tc.model)
				if !ok || cached != entry {
					t.Fatalf("cached entry = %+v, found=%v", cached, ok)
				}
				intrinsic, ok := source.LookupIntrinsic(t.Context(), "gateway/"+tc.model)
				if !ok || intrinsic != tc.intrinsic {
					t.Fatalf("intrinsic = %+v, found=%v", intrinsic, ok)
				}
				if intrinsic, ok := source.LookupIntrinsicCached("gateway/" + tc.model); !ok || intrinsic != tc.intrinsic {
					t.Fatalf("cached intrinsic = %+v, found=%v", intrinsic, ok)
				}
				e, ok := source.LookupEvidence(t.Context(), "google", tc.model)
				if !ok || e.Pricing != tc.prices || e.Tools != tc.tools || e.Limits != entry.Evidence.Limits {
					t.Fatalf("%s evidence = %+v, found=%v", tc.model, e, ok)
				}
				record, found := source.LookupModelCached("google", llm.ModelID(tc.model))
				if !found || record.Information != entry.Information || record.Information.Tools != e.Tools || record.Information.Reasoning != e.Reasoning || record.Information.Limits != e.Limits || record.Provenance != e.Provenance {
					t.Fatalf("C1 record = %+v, found=%v, evidence=%+v", record, found, e)
				}
				if cached, ok := source.LookupEvidenceCached("google", tc.model); !ok || cached != e {
					t.Fatalf("cached evidence = %+v, found=%v", cached, ok)
				}
			}
			if restored.Entries["google"]["false-zero"] != original.Entries["google"]["false-zero"] || restored.Entries["google"]["supported"] != original.Entries["google"]["supported"] {
				t.Fatal("cache-owned entries were normalized in place")
			}
			delete(restored.Entries["google"], "false-zero")
			if e, ok := source.LookupEvidenceCached("google", "false-zero"); !ok || !e.Pricing.InputKnown {
				t.Fatalf("cache mutation changed source = %+v, found=%v", e, ok)
			}
			if store.gets.Load() != 1 {
				t.Fatalf("cache reads = %d, want 1", store.gets.Load())
			}
		})
	}
}

func TestSnapshotNonfinitePricesAreUnknown(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, input := range []bool{false, true} {
			prices := modelsdev.PriceEvidence{InputKnown: true, OutputKnown: true}
			if input {
				prices.InputCostPerMTok = rate
			} else {
				prices.OutputCostPerMTok = rate
			}
			store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
			if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
				Schema: 4, FetchedAt: time.Now(),
				Entries: map[string]map[string]modelsdev.Entry{"p": {"m": {Evidence: modelsdev.Evidence{Pricing: prices}}}},
			}); err != nil {
				t.Fatal(err)
			}
			e, ok := modelsdev.New(store).LookupEvidence(t.Context(), "p", "m")
			want := modelsdev.PriceEvidence{InputKnown: !input, OutputKnown: input}
			if !ok || e.Pricing != want {
				t.Errorf("rate=%v input=%v: prices=%+v found=%v, want %+v", rate, input, e.Pricing, ok, want)
			}
		}
	}
}

func TestSnapshotStaleProjectionUsesValidatedEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
		Schema: 4, FetchedAt: time.Now().Add(-24 * time.Hour),
		Entries: map[string]map[string]modelsdev.Entry{"google": {"m": {Intrinsic: modelsdev.Intrinsic{SupportsTools: true}, Evidence: modelsdev.Evidence{Tools: llm.Supports.SUPPORTUNSUPPORTED}}}},
	}); err != nil {
		t.Fatal(err)
	}
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL))
	if err := source.Warm(t.Context()); err == nil {
		t.Fatal("refresh did not report its failure")
	}
	if e, ok := source.LookupCached("gemini", "m"); !ok || e.SupportsTools {
		t.Fatalf("stale legacy projection = %+v, found=%v", e, ok)
	}
	if e, ok := source.LookupEvidenceCached("google", "m"); !ok || e.Tools != llm.Supports.SUPPORTUNSUPPORTED {
		t.Fatalf("stale evidence = %+v, found=%v", e, ok)
	}
}

func TestSchemaThreeVariantsRejectedIncludingStaleFallback(t *testing.T) {
	yes := llm.Supports.SUPPORTSUPPORTED
	for _, stale := range []bool{false, true} {
		for _, refresh := range []bool{false, true} {
			for _, variant := range []string{"information", "evidence"} {
				t.Run(fmt.Sprintf("%s/stale=%v/refresh=%v", variant, stale, refresh), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if !refresh {
							http.Error(w, "offline", http.StatusBadRequest)
							return
						}
						_, _ = w.Write([]byte(`{"google":{"models":{"fresh":{}}}}`))
					}))
					t.Cleanup(server.Close)
					entry := modelsdev.Entry{Intrinsic: modelsdev.Intrinsic{SupportsTools: true}, Pricing: modelsdev.Pricing{InputCostPerMTok: 999}}
					if variant == "information" {
						entry.Information = modelsdev.ModelInformation{Tools: yes}
					} else {
						entry.Evidence = modelsdev.Evidence{Tools: yes, Pricing: modelsdev.PriceEvidence{InputKnown: true}}
					}
					observed := time.Now()
					if stale {
						observed = observed.Add(-24 * time.Hour)
					}
					store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
					if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{Schema: 3, FetchedAt: observed, Entries: map[string]map[string]modelsdev.Entry{"google": {"old": entry}}}); err != nil {
						t.Fatal(err)
					}
					source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL))
					err := source.Warm(t.Context())
					if (err == nil) != refresh {
						t.Fatalf("Warm = %v, refresh=%v", err, refresh)
					}
					if _, ok := source.LookupCached("gemini", "old"); ok {
						t.Fatal("schema 3 leaked into legacy view")
					}
					if _, ok := source.LookupEvidenceCached("google", "old"); ok {
						t.Fatal("schema 3 leaked into evidence view")
					}
					if _, ok := source.LookupModelCached("google", "old"); ok {
						t.Fatal("schema 3 leaked into C1 view")
					}
					e, ok := source.LookupEvidenceCached("google", "fresh")
					if ok != refresh || (ok && (e.SchemaVersion != 4 || e.Tools != llm.Supports.SUPPORTUNKNOWN || e.Pricing.InputKnown)) {
						t.Fatalf("replacement = %+v, found=%v", e, ok)
					}
				})
			}
		}
	}
}

func TestMalformedRefreshKeepsProcessLocalAndPersistedSnapshot(t *testing.T) {
	var payload atomic.Value
	payload.Store(`{"google":{"models":{"m":{"tool_call":true,"cost":{"input":0}}}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload.Load().(string))) }))
	t.Cleanup(server.Close)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(-time.Minute))
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, _ := source.LookupEvidenceCached("google", "m")
	record, _ := source.LookupModelCached("google", "m")
	payload.Store(`{"google":{"models":{"good":{"tool_call":false},"m":{"cost":{"input":"NaN"}}}}}`)
	if err := source.Warm(t.Context()); err == nil {
		t.Fatal("malformed refresh succeeded")
	}
	if got, ok := source.LookupEvidenceCached("google", "m"); !ok || got != retained {
		t.Fatalf("last good = %+v, found=%v", got, ok)
	}
	if got, ok := source.LookupModelCached("google", "m"); !ok || got != record {
		t.Fatalf("last good C1 = %+v, found=%v", got, ok)
	}
	if _, ok := source.LookupCached("google", "good"); ok {
		t.Fatal("partially published failed refresh")
	}
	snapshot, err := store.Get(t.Context(), "models.dev")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != retained.Provenance.Revision || snapshot.FetchedAt != retained.Provenance.ObservedAt || snapshot.Entries["google"]["m"].Evidence.Tools != retained.Tools {
		t.Fatal("failed refresh replaced persisted snapshot")
	}
}

func TestNativeModalityDecoderAndAllViewsAgree(t *testing.T) {
	source, _, _ := evidenceSource(t, `{"m":{"modalities":{"input":["TEXT","Image","aUdIo","VIDEO","PdF","future"],"output":["PDF"]}}}`)
	e, ok := source.LookupEvidence(t.Context(), "google", "m")
	yes, no := llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED
	if !ok || e.Modalities.Input != (modelsdev.ModalityEvidence{Text: yes, Image: yes, Audio: yes, Video: yes, PDF: yes}) || e.Modalities.Output != (modelsdev.ModalityEvidence{Text: no, Image: no, Audio: no, Video: no, PDF: yes}) {
		t.Fatalf("native modalities = %+v", e)
	}
	record, ok := source.LookupModelCached("google", "m")
	want := llm.DirectionalModalities{Input: llm.ModalitySupport{Text: yes, Image: yes, Audio: yes, Video: yes}, Output: llm.ModalitySupport{Text: no, Image: no, Audio: no, Video: no}}
	if !ok || record.Information.Modalities != want || record.Provenance != e.Provenance {
		t.Fatalf("C1 = %+v", record)
	}
	legacy, ok := source.LookupCached("gemini", "m")
	if !ok || !legacy.SupportsVision || !legacy.SupportsVideo || legacy.Information != record.Information || legacy.Evidence.Modalities != e.Modalities {
		t.Fatalf("legacy = %+v", legacy)
	}
}
