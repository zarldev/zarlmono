package modelsdev_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func evidenceSource(t *testing.T, models string) (*modelsdev.Source, *countingSnapshotCache, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"google":{"models":` + models + `}}`))
	}))
	t.Cleanup(server.Close)
	store := &countingSnapshotCache{inner: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	return modelsdev.New(store, modelsdev.WithBaseURL(server.URL)), store, &requests
}

func TestEvidencePresenceAndLegacyProjection(t *testing.T) {
	source, _, _ := evidenceSource(t, `{
		"absent": {},
		"false-zero": {"tool_call":false,"reasoning":false,"cost":{"input":0,"output":0}},
		"true-partial": {"tool_call":true,"reasoning":true,"cost":{"output":2.5}},
		"null": {"tool_call":null,"reasoning":null,"cost":{"input":null,"output":null}}
	}`)
	for _, tc := range []struct {
		model       string
		support     llm.Support
		inputKnown  bool
		outputKnown bool
		outputRate  float64
	}{
		{"absent", llm.Supports.SUPPORTUNKNOWN, false, false, 0},
		{"false-zero", llm.Supports.SUPPORTUNSUPPORTED, true, true, 0},
		{"true-partial", llm.Supports.SUPPORTSUPPORTED, false, true, 2.5},
		{"null", llm.Supports.SUPPORTUNKNOWN, false, false, 0},
	} {
		t.Run(tc.model, func(t *testing.T) {
			e, ok := source.LookupEvidence(t.Context(), "google", tc.model)
			if !ok || e.Tools != tc.support || e.Reasoning != tc.support {
				t.Fatalf("evidence = %+v, found=%v, want support=%v", e, ok, tc.support)
			}
			if e.Pricing.InputKnown != tc.inputKnown || e.Pricing.OutputKnown != tc.outputKnown || e.Pricing.OutputCostPerMTok != tc.outputRate {
				t.Fatalf("prices = %+v", e.Pricing)
			}
			legacy, ok := source.Lookup(t.Context(), "gemini", tc.model)
			if !ok || legacy.SupportsTools != (tc.support == llm.Supports.SUPPORTSUPPORTED) || legacy.SupportsThinking != legacy.SupportsTools || legacy.OutputCostPerMTok != tc.outputRate {
				t.Fatalf("legacy projection = %+v, found=%v", legacy, ok)
			}
		})
	}
}

func TestEvidenceDirectionalModalitiesAndIndependentLimits(t *testing.T) {
	source, _, _ := evidenceSource(t, `{
		"directions":{"modalities":{"input":["text","IMAGE","pdf"],"output":["audio","video"]},"limit":{"input":123,"output":45,"context":200}},
		"input-only":{"limit":{"input":123}},
		"output-only":{"limit":{"output":45}},
		"context-only":{"limit":{"context":200}},
		"generation":{"modalities":{"output":["image","pdf"]}},
		"empty":{"modalities":{"input":[],"output":["future-hologram"]}},
		"absent":{}
	}`)
	e, ok := source.LookupEvidence(t.Context(), "google", "directions")
	if !ok {
		t.Fatal("missing directional evidence")
	}
	yes, no := llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED
	want := modelsdev.DirectionalModalities{
		Input:  modelsdev.ModalityEvidence{Text: yes, Image: yes, PDF: yes, Audio: no, Video: no},
		Output: modelsdev.ModalityEvidence{Text: no, Image: no, PDF: no, Audio: yes, Video: yes},
	}
	if e.Modalities != want || e.Limits != (llm.ModelLimits{InputTokens: 123, OutputTokens: 45, ContextTokens: 200}) {
		t.Fatalf("directional evidence = %+v", e)
	}
	for _, tc := range []struct {
		model string
		want  llm.ModelLimits
	}{
		{"input-only", llm.ModelLimits{InputTokens: 123}},
		{"output-only", llm.ModelLimits{OutputTokens: 45}},
		{"context-only", llm.ModelLimits{ContextTokens: 200}},
	} {
		e, ok := source.LookupEvidence(t.Context(), "google", tc.model)
		if !ok || e.Limits != tc.want {
			t.Errorf("%s limits = %+v, found=%v, want %+v", tc.model, e.Limits, ok, tc.want)
		}
	}
	empty, _ := source.LookupEvidence(t.Context(), "google", "empty")
	unsupported := modelsdev.ModalityEvidence{Text: no, Image: no, PDF: no, Audio: no, Video: no}
	if empty.Modalities.Input != unsupported || empty.Modalities.Output != unsupported {
		t.Fatalf("empty/unknown modalities = %+v", empty.Modalities)
	}
	generation, _ := source.LookupEvidence(t.Context(), "google", "generation")
	if generation.Modalities.Input != (modelsdev.ModalityEvidence{}) || generation.Modalities.Output.Image != yes || generation.Modalities.Output.PDF != yes {
		t.Fatalf("generation modalities = %+v", generation.Modalities)
	}
	absent, _ := source.LookupEvidence(t.Context(), "google", "absent")
	if absent.Modalities != (modelsdev.DirectionalModalities{}) {
		t.Fatalf("absent modalities = %+v", absent.Modalities)
	}
}

func TestEvidenceExactKeysAndCachedNoIO(t *testing.T) {
	source, store, requests := evidenceSource(t, `{"Model":{"tool_call":true}}`)
	if _, ok := source.LookupEvidenceCached("google", "Model"); ok || store.gets.Load() != 0 || requests.Load() != 0 {
		t.Fatal("cold cached lookup loaded a snapshot")
	}
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	reads, calls := store.gets.Load(), requests.Load()
	for _, keys := range [][2]string{{"gemini", "Model"}, {"google-vertex", "Model"}, {"google", "gateway/Model"}, {"google", "model"}, {"google", " Model "}, {"other", "Model"}} {
		if _, ok := source.LookupEvidence(t.Context(), keys[0], keys[1]); ok {
			t.Errorf("exact evidence unexpectedly matched %v", keys)
		}
		if _, ok := source.LookupEvidenceCached(keys[0], keys[1]); ok {
			t.Errorf("cached exact evidence unexpectedly matched %v", keys)
		}
	}
	if _, ok := source.LookupIntrinsic(t.Context(), "gateway/model"); !ok {
		t.Fatal("legacy basename fallback changed")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 20 {
				if _, ok := source.LookupEvidenceCached("google", "Model"); !ok {
					t.Error("warm cached lookup missed")
				}
			}
		})
	}
	wg.Wait()
	if store.gets.Load() != reads || requests.Load() != calls {
		t.Fatal("cached evidence performed I/O")
	}
}

func TestEvidenceSnapshotPersistenceProvenanceAndOwnership(t *testing.T) {
	source, store, _ := evidenceSource(t, `{"model":{"tool_call":false,"cost":{"input":0},"modalities":{"input":["pdf"]}}}`)
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Get(t.Context(), "models.dev")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != 4 {
		t.Fatalf("schema = %d, want presence-preserving version 4", snap.Schema)
	}
	encoded, err := json.Marshal(snap) //nolint:musttag // Versioned Snapshot retains the existing exported-field JSON format, including C1 domain values.
	if err != nil {
		t.Fatal(err)
	}
	var restored modelsdev.Snapshot
	if err := json.Unmarshal(encoded, &restored); err != nil { //nolint:musttag // Exercise the actual versioned Snapshot cache format, not a parallel test schema.
		t.Fatal(err)
	}
	memory := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	if err := memory.Set(t.Context(), "models.dev", restored); err != nil {
		t.Fatal(err)
	}
	loaded := modelsdev.New(memory)
	e, ok := loaded.LookupEvidence(t.Context(), "google", "model")
	if !ok || e.Tools != llm.Supports.SUPPORTUNSUPPORTED || !e.Pricing.InputKnown || e.Pricing.OutputKnown || e.Modalities.Input.PDF != llm.Supports.SUPPORTSUPPORTED {
		t.Fatalf("round-trip evidence = %+v, found=%v", e, ok)
	}
	want := llm.MetadataProvenance{Source: llm.MetadataSources.METADATACATALOGUE, Snapshot: "models.dev", Revision: restored.Revision, ObservedAt: restored.FetchedAt, ExpiresAt: restored.FetchedAt.Add(6 * time.Hour)}
	if e.Provenance != want || e.SchemaVersion != restored.Schema {
		t.Fatalf("provenance = %+v, want %+v", e.Provenance, want)
	}
	original := e
	e.Tools = llm.Supports.SUPPORTSUPPORTED
	e.Pricing.InputKnown = false
	delete(snap.Entries["google"], "model")     // Cache writes must not expose Source maps.
	delete(restored.Entries["google"], "model") // Cache reads must not retain cache maps.
	for _, s := range []*modelsdev.Source{source, loaded} {
		again, ok := s.LookupEvidenceCached("google", "model")
		if !again.Provenance.ObservedAt.Equal(original.Provenance.ObservedAt) {
			t.Fatal("snapshot observation time changed")
		}
		if !again.Provenance.ExpiresAt.Equal(original.Provenance.ExpiresAt) {
			t.Fatal("snapshot expiry changed")
		}
		again.Provenance.ExpiresAt = original.Provenance.ExpiresAt
		again.Provenance.ObservedAt = original.Provenance.ObservedAt
		if !ok || again != original {
			t.Fatalf("mutated retained evidence = %+v, found=%v", again, ok)
		}
	}
}

func TestEvidenceRejectsOldPresenceLossySnapshot(t *testing.T) {
	for _, schema := range []int{1, 2} {
		source, store, requests := evidenceSource(t, `{"new":{"tool_call":false,"cost":{"input":0}}}`)
		old := modelsdev.Snapshot{Schema: schema, FetchedAt: time.Now(), Entries: map[string]map[string]modelsdev.Entry{"google": {"old": {}}}}
		if err := store.Set(t.Context(), "models.dev", old); err != nil {
			t.Fatal(err)
		}
		if _, ok := source.LookupEvidence(t.Context(), "google", "old"); ok {
			t.Fatalf("schema %d snapshot was accepted", schema)
		}
		if e, ok := source.LookupEvidenceCached("google", "new"); !ok || e.Tools != llm.Supports.SUPPORTUNSUPPORTED || !e.Pricing.InputKnown || requests.Load() != 1 {
			t.Fatalf("schema %d replacement = %+v, found=%v, requests=%d", schema, e, ok, requests.Load())
		}
	}
}

func TestEvidenceCachedServesStaleWithoutIO(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"p":{"models":{"m":{"tool_call":false}}}}`))
	}))
	t.Cleanup(server.Close)
	store := &countingSnapshotCache{inner: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(-time.Second))
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	reads := store.gets.Load()
	for range 10 {
		e, ok := source.LookupEvidenceCached("p", "m")
		if !ok || e.Tools != llm.Supports.SUPPORTUNSUPPORTED {
			t.Fatalf("stale cached evidence = %+v, found=%v", e, ok)
		}
	}
	if store.gets.Load() != reads || requests.Load() != 1 {
		t.Fatal("stale cached lookup performed I/O")
	}
}

func TestEvidenceDoesNotServeOldSnapshotWhenFetchFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
		Schema: 2, FetchedAt: time.Now(),
		Entries: map[string]map[string]modelsdev.Entry{"p": {"m": {Intrinsic: modelsdev.Intrinsic{SupportsTools: true}}}},
	}); err != nil {
		t.Fatal(err)
	}
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL))
	if err := source.Warm(t.Context()); err == nil {
		t.Fatal("fetch failure was not reported")
	}
	if _, ok := source.LookupEvidenceCached("p", "m"); ok {
		t.Fatal("old snapshot admitted as stale evidence")
	}
	if _, ok := source.LookupCached("p", "m"); ok {
		t.Fatal("old snapshot admitted as legacy data")
	}
}
