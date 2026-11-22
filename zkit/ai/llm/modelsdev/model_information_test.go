package modelsdev_test

import (
	"crypto/sha256"
	"encoding/hex"
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

func informationSource(t *testing.T, payload string) *modelsdev.Source {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL))
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestModelInformationBooleanEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		wire   string
		want   llm.Support
		legacy bool
	}{
		{name: "missing", wire: `{}`, want: llm.Supports.SUPPORTUNKNOWN},
		{name: "null", wire: `{"tool_call":null,"reasoning":null}`, want: llm.Supports.SUPPORTUNKNOWN},
		{name: "false", wire: `{"tool_call":false,"reasoning":false}`, want: llm.Supports.SUPPORTUNSUPPORTED},
		{name: "true", wire: `{"tool_call":true,"reasoning":true}`, want: llm.Supports.SUPPORTSUPPORTED, legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := informationSource(t, `{"provider":{"models":{"model":`+test.wire+`}}}`)
			record, ok := source.LookupModelCached("provider", "model")
			if !ok || record.Information.Tools != test.want || record.Information.Reasoning != test.want {
				t.Fatalf("record = %+v, ok = %v, want boolean evidence %v", record, ok, test.want)
			}
			entry, ok := source.LookupCached("provider", "model")
			if !ok || entry.Information != record.Information || entry.SupportsTools != test.legacy || entry.SupportsThinking != test.legacy {
				t.Fatalf("legacy entry = %+v, ok = %v", entry, ok)
			}
		})
	}
}

func TestModelInformationDirectionalModalities(t *testing.T) {
	no := llm.Supports.SUPPORTUNSUPPORTED
	yes := llm.Supports.SUPPORTSUPPORTED
	empty := llm.ModalitySupport{Text: no, Image: no, Audio: no, Video: no}
	for _, test := range []struct {
		name string
		wire string
		want llm.DirectionalModalities
	}{
		{name: "missing", wire: `{}`},
		{name: "null modalities", wire: `{"modalities":null}`},
		{name: "missing lists", wire: `{"modalities":{}}`},
		{name: "null lists", wire: `{"modalities":{"input":null,"output":null}}`},
		{name: "empty input", wire: `{"modalities":{"input":[]}}`, want: llm.DirectionalModalities{Input: empty}},
		{name: "empty output", wire: `{"modalities":{"output":[]}}`, want: llm.DirectionalModalities{Output: empty}},
		{name: "text input", wire: `{"modalities":{"input":["text"]}}`, want: llm.DirectionalModalities{Input: llm.ModalitySupport{Text: yes, Image: no, Audio: no, Video: no}}},
		{name: "image input", wire: `{"modalities":{"input":["image"]}}`, want: llm.DirectionalModalities{Input: llm.ModalitySupport{Text: no, Image: yes, Audio: no, Video: no}}},
		{name: "audio input", wire: `{"modalities":{"input":["audio"]}}`, want: llm.DirectionalModalities{Input: llm.ModalitySupport{Text: no, Image: no, Audio: yes, Video: no}}},
		{name: "video input", wire: `{"modalities":{"input":["video"]}}`, want: llm.DirectionalModalities{Input: llm.ModalitySupport{Text: no, Image: no, Audio: no, Video: yes}}},
		{name: "text output", wire: `{"modalities":{"output":["text"]}}`, want: llm.DirectionalModalities{Output: llm.ModalitySupport{Text: yes, Image: no, Audio: no, Video: no}}},
		{name: "image output", wire: `{"modalities":{"output":["image"]}}`, want: llm.DirectionalModalities{Output: llm.ModalitySupport{Text: no, Image: yes, Audio: no, Video: no}}},
		{name: "audio output", wire: `{"modalities":{"output":["audio"]}}`, want: llm.DirectionalModalities{Output: llm.ModalitySupport{Text: no, Image: no, Audio: yes, Video: no}}},
		{name: "video output", wire: `{"modalities":{"output":["video"]}}`, want: llm.DirectionalModalities{Output: llm.ModalitySupport{Text: no, Image: no, Audio: no, Video: yes}}},
		{name: "future modality", wire: `{"modalities":{"input":["future"]}}`, want: llm.DirectionalModalities{Input: empty}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := informationSource(t, `{"provider":{"models":{"model":`+test.wire+`}}}`)
			record, ok := source.LookupModelCached("provider", "model")
			if !ok || record.Information.Modalities != test.want {
				t.Fatalf("modalities = %+v, ok = %v, want %+v", record.Information.Modalities, ok, test.want)
			}
			entry, _ := source.LookupCached("provider", "model")
			if entry.SupportsVision != (test.want.Input.Image == yes) || entry.SupportsVideo != (test.want.Input.Video == yes) {
				t.Fatalf("legacy input modalities = %+v", entry.Intrinsic)
			}
		})
	}
}

func TestModelInformationIndependentLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		wire string
		want llm.ModelLimits
	}{
		{name: "missing", wire: `{}`},
		{name: "null", wire: `{"limit":null}`},
		{name: "zero", wire: `{"limit":{"input":0,"output":0,"context":0}}`},
		{name: "input only", wire: `{"limit":{"input":101}}`, want: llm.ModelLimits{InputTokens: 101}},
		{name: "output only", wire: `{"limit":{"output":202}}`, want: llm.ModelLimits{OutputTokens: 202}},
		{name: "context only", wire: `{"limit":{"context":303}}`, want: llm.ModelLimits{ContextTokens: 303}},
		{name: "all", wire: `{"limit":{"input":101,"output":202,"context":303}}`, want: llm.ModelLimits{InputTokens: 101, OutputTokens: 202, ContextTokens: 303}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := informationSource(t, `{"provider":{"models":{"model":`+test.wire+`}}}`)
			record, ok := source.LookupModelCached("provider", "model")
			if !ok || record.Information.Limits != test.want {
				t.Fatalf("limits = %+v, ok = %v, want %+v", record.Information.Limits, ok, test.want)
			}
			entry, _ := source.LookupCached("provider", "model")
			if int64(entry.ContextWindow) != test.want.ContextTokens || int64(entry.MaxOutputTokens) != test.want.OutputTokens {
				t.Fatalf("legacy limits = %+v", entry.Intrinsic)
			}
		})
	}
}

func TestModelInformationSkipsInvalidRecords(t *testing.T) {
	for _, wire := range []string{
		`"invalid"`, `42`, `true`, `[]`, `[{"cost":{"input":"NaN"}}]`,
		`{"tool_call":"true"}`, `{"modalities":{"input":"image"}}`,
		`null`, `{"modalities":{"input":[null]}}`, `{"modalities":{"output":["text",null]}}`,
	} {
		t.Run(wire, func(t *testing.T) {
			source := informationSource(t, `{"provider":{"models":{"bad":`+wire+`,"good":{"tool_call":true}}}}`)
			if _, ok := source.LookupModelCached("provider", "bad"); ok {
				t.Fatal("invalid model record accepted")
			}
			if _, ok := source.LookupCached("provider", "bad"); ok {
				t.Fatal("invalid model record leaked to legacy lookup")
			}
			if _, ok := source.LookupModelCached("provider", "good"); !ok {
				t.Fatal("invalid model discarded valid neighboring model")
			}
		})
	}
}

func TestModelInformationExactIdentitiesAndLegacyAliases(t *testing.T) {
	source := informationSource(t, `{"google":{"models":{"Owner/Model":{"tool_call":true},"owner/model":{"tool_call":false}}}}`)
	for _, test := range []struct {
		provider string
		model    llm.ModelID
		found    bool
	}{
		{provider: "google", model: "Owner/Model", found: true},
		{provider: "google", model: "owner/model", found: true},
		{provider: "gemini", model: "Owner/Model"},
		{provider: "google-vertex", model: "Owner/Model"},
		{provider: "Google", model: "Owner/Model"},
		{provider: " google", model: "Owner/Model"},
		{provider: "google", model: "OWNER/MODEL"},
		{provider: "google", model: "Owner/Model "},
		{provider: "google", model: "Model"},
		{provider: "google", model: "gateway/Owner/Model"},
	} {
		t.Run(test.provider+"/"+string(test.model), func(t *testing.T) {
			if _, ok := source.LookupModelCached(test.provider, test.model); ok != test.found {
				t.Fatalf("found = %v, want %v", ok, test.found)
			}
		})
	}
	upper, _ := source.LookupModelCached("google", "Owner/Model")
	lower, _ := source.LookupModelCached("google", "owner/model")
	if upper.Information.Tools != llm.Supports.SUPPORTSUPPORTED || lower.Information.Tools != llm.Supports.SUPPORTUNSUPPORTED {
		t.Fatal("case-distinct identities were conflated")
	}
	for _, provider := range []string{"gemini", "google-vertex"} {
		entry, ok := source.Lookup(t.Context(), provider, "Owner/Model")
		if !ok || !entry.SupportsTools {
			t.Fatalf("legacy alias %q = %+v, ok = %v", provider, entry, ok)
		}
	}
}

func TestModelInformationCachedLookupDoesNoIO(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"provider":{"models":{"model":{}}}}`))
	}))
	t.Cleanup(server.Close)
	store := &countingSnapshotCache{inner: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(-time.Minute))
	for range 5 {
		if _, ok := source.LookupModelCached("provider", "model"); ok {
			t.Fatal("unwarmed source returned metadata")
		}
	}
	if store.gets.Load() != 0 || requests.Load() != 0 {
		t.Fatal("unwarmed cached lookup performed I/O")
	}
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	gets, fetches := store.gets.Load(), requests.Load()
	for range 5 {
		record, ok := source.LookupModelCached("provider", "model")
		if !ok || !record.Provenance.ExpiresAt.Before(time.Now()) {
			t.Fatalf("stale evidence not observable: %+v, ok = %v", record, ok)
		}
	}
	if store.gets.Load() != gets || requests.Load() != fetches {
		t.Fatal("stale cached lookup performed I/O")
	}
}

func TestModelInformationProvenancePersistenceAndRetainedSnapshots(t *testing.T) {
	const first = `{"provider":{"models":{"model":{"tool_call":true,"modalities":{"input":["image"]},"limit":{"input":12}}}}}`
	const second = `{"provider":{"models":{"model":{"tool_call":false,"modalities":{"input":[]},"limit":{"output":34}}}}}`
	var payload atomic.Value
	payload.Store(first)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload.Load().(string)))
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	store, err := cache.NewPersistentFileCache[string, modelsdev.Snapshot](directory)
	if err != nil {
		t.Fatal(err)
	}
	ttl := -time.Minute
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(ttl))
	before := time.Now()
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, ok := source.LookupModelCached("provider", "model")
	if !ok {
		t.Fatal("initial evidence missing")
	}
	original := retained
	digest := sha256.Sum256([]byte(first))
	provenance := retained.Provenance
	if provenance.Source != llm.MetadataSources.METADATACATALOGUE || provenance.Snapshot != "models.dev" || provenance.Revision != hex.EncodeToString(digest[:]) || provenance.ObservedAt.Before(before) || provenance.ObservedAt.After(time.Now()) || provenance.ExpiresAt != provenance.ObservedAt.Add(ttl) {
		t.Fatalf("provenance = %+v", provenance)
	}
	// Caller mutation and source refresh must not change each other's values.
	retained.Information.Tools = llm.Supports.SUPPORTUNKNOWN
	retained.Provenance.Revision = "caller-owned"
	again, _ := source.LookupModelCached("provider", "model")
	if again != original {
		t.Fatal("caller mutation changed source")
	}
	payload.Store(second)
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, _ := source.LookupModelCached("provider", "model")
	if fresh.Information.Tools != llm.Supports.SUPPORTUNSUPPORTED || fresh.Information.Limits != (llm.ModelLimits{OutputTokens: 34}) || fresh.Provenance.Revision == original.Provenance.Revision || again != original {
		t.Fatalf("refresh = %+v; retained = %+v", fresh, again)
	}
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	sameContent, _ := source.LookupModelCached("provider", "model")
	if sameContent.Provenance.Revision != fresh.Provenance.Revision {
		t.Fatal("same content changed revision")
	}
	snapshot, err := store.Get(t.Context(), "models.dev")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Schema != 4 || snapshot.Revision != fresh.Provenance.Revision || !snapshot.FetchedAt.Equal(sameContent.Provenance.ObservedAt) {
		t.Fatalf("persisted snapshot identity = %+v", snapshot)
	}
	reopened, err := cache.NewPersistentFileCache[string, modelsdev.Snapshot](directory)
	if err != nil {
		t.Fatal(err)
	}
	restarted := modelsdev.New(reopened, modelsdev.WithTTL(time.Hour), modelsdev.WithBaseURL(":invalid"))
	if err := restarted.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	restored, _ := restarted.LookupModelCached("provider", "model")
	if restored.Information != fresh.Information || restored.Provenance.Revision != fresh.Provenance.Revision || !restored.Provenance.ObservedAt.Equal(sameContent.Provenance.ObservedAt) {
		t.Fatalf("restored evidence = %+v", restored)
	}
}

func TestModelInformationSchemaTwoInvalidation(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh failure", true: "refresh success"}[refresh], func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if !refresh {
					http.Error(w, "offline", http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"google":{"models":{"model":{}}}}`))
			}))
			t.Cleanup(server.Close)
			store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
			if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
				Schema: 2, FetchedAt: time.Now(),
				Entries: map[string]map[string]modelsdev.Entry{"google": {"model": {Intrinsic: modelsdev.Intrinsic{SupportsTools: false, SupportsThinking: false}}}},
			}); err != nil {
				t.Fatal(err)
			}
			source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL))
			if _, ok := source.LookupModelCached("google", "model"); ok || requests.Load() != 0 {
				t.Fatal("cached lookup loaded old snapshot")
			}
			err := source.Warm(t.Context())
			if refresh && err != nil {
				t.Fatal(err)
			}
			if !refresh && err == nil {
				t.Fatal("refresh failure not reported")
			}
			if requests.Load() != 1 {
				t.Fatalf("refresh requests = %d, want 1", requests.Load())
			}
			record, ok := source.LookupModelCached("google", "model")
			if ok != refresh || record.Information != (modelsdev.ModelInformation{}) {
				t.Fatalf("old false fields became evidence: %+v, ok = %v", record, ok)
			}
			if !refresh {
				if _, ok := source.LookupCached("google", "model"); ok {
					t.Fatal("invalid schema leaked into legacy lookup")
				}
			}
		})
	}
}

func TestModelInformationStaleEvidenceKeepsProvenanceAfterFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	observed := time.Now().Add(-time.Hour)
	if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
		Schema: 4, FetchedAt: observed, Revision: "retained-revision",
		Entries: map[string]map[string]modelsdev.Entry{"provider": {"model": {Evidence: modelsdev.Evidence{Tools: llm.Supports.SUPPORTUNSUPPORTED}}}},
	}); err != nil {
		t.Fatal(err)
	}
	source := modelsdev.New(store, modelsdev.WithTTL(time.Minute), modelsdev.WithBaseURL(server.URL))
	if err := source.Warm(t.Context()); err == nil {
		t.Fatal("refresh failure not reported")
	}
	record, ok := source.LookupModelCached("provider", "model")
	if !ok || record.Information.Tools != llm.Supports.SUPPORTUNSUPPORTED || record.Provenance.Revision != "retained-revision" || record.Provenance.ObservedAt != observed || record.Provenance.ExpiresAt != observed.Add(time.Minute) {
		t.Fatalf("stale evidence = %+v, ok = %v", record, ok)
	}
}

func TestModelInformationRejectsMismatchedEmbeddedIDs(t *testing.T) {
	for _, test := range []struct {
		name   string
		models string
	}{
		{name: "mismatch", models: `"outer":{"id":"inner","tool_call":true}`},
		{name: "colliding mismatches", models: `"outer":{"id":"inner","tool_call":true},"other":{"id":"inner","tool_call":false}`},
		{name: "collision with valid neighbor", models: `"outer":{"id":"good","tool_call":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := informationSource(t, `{"provider":{"models":{`+test.models+`,"good":{"id":"good","tool_call":true},"omitted":{"tool_call":true},"empty":{"id":"","tool_call":true}}}}`)
			for _, model := range []llm.ModelID{"outer", "other", "inner"} {
				if _, ok := source.LookupModelCached("provider", model); ok {
					t.Fatalf("invalid identity %q accepted", model)
				}
				if _, ok := source.LookupCached("provider", string(model)); ok {
					t.Fatalf("invalid identity %q leaked to legacy lookup", model)
				}
			}
			for _, model := range []llm.ModelID{"good", "omitted", "empty"} {
				record, ok := source.LookupModelCached("provider", model)
				if !ok || record.Information.Tools != llm.Supports.SUPPORTSUPPORTED {
					t.Fatalf("valid neighbor %q = %+v, ok = %v", model, record, ok)
				}
				entry, ok := source.Lookup(t.Context(), "provider", string(model))
				if !ok || !entry.SupportsTools {
					t.Fatalf("valid legacy neighbor %q = %+v, ok = %v", model, entry, ok)
				}
			}
		})
	}
}

func TestModelInformationOwnsSnapshotMaps(t *testing.T) {
	for _, test := range []struct {
		name  string
		seed  bool
		stale bool
	}{
		{name: "seeded cache", seed: true},
		{name: "stale seeded cache after refresh failure", seed: true, stale: true},
		{name: "fetched cache"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.stale {
					http.Error(w, "offline", http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"nested":{"models":{"model":{"tool_call":true}}},"outer":{"models":{"model":{"tool_call":true}}}}`))
			}))
			t.Cleanup(server.Close)
			store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
			if test.seed {
				observed := time.Now()
				if test.stale {
					observed = observed.Add(-24 * time.Hour)
				}
				if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
					Schema: 4, FetchedAt: observed, Revision: "seeded-revision",
					Entries: map[string]map[string]modelsdev.Entry{
						"nested": {"model": {Evidence: modelsdev.Evidence{Tools: llm.Supports.SUPPORTSUPPORTED}}},
						"outer":  {"model": {Evidence: modelsdev.Evidence{Tools: llm.Supports.SUPPORTSUPPORTED}}},
					},
				}); err != nil {
					t.Fatal(err)
				}
			}
			source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL))
			err := source.Warm(t.Context())
			if (err != nil) != test.stale {
				t.Fatalf("Warm = %v, stale refresh failure expected = %v", err, test.stale)
			}
			snapshot, err := store.Get(t.Context(), "models.dev")
			if err != nil {
				t.Fatal(err)
			}
			retained, ok := source.LookupModelCached("nested", "model")
			if !ok || retained.Information.Tools != llm.Supports.SUPPORTSUPPORTED {
				t.Fatalf("initial evidence = %+v, ok = %v", retained, ok)
			}
			original := retained
			assertUnchanged := func() {
				for _, provider := range []string{"nested", "outer"} {
					record, found := source.LookupModelCached(provider, "model")
					if !found || record != original || retained != original {
						t.Errorf("cache mutation changed %q evidence: %+v, found = %v; retained = %+v", provider, record, found, retained)
					}
					entry, found := source.LookupCached(provider, "model")
					if !found || entry.Information != original.Information {
						t.Errorf("cache mutation changed %q legacy entry: %+v, found = %v", provider, entry, found)
					}
				}
			}
			nested := snapshot.Entries["nested"]
			nested["model"] = modelsdev.Entry{}
			snapshot.Entries["outer"] = map[string]modelsdev.Entry{"model": {}}
			assertUnchanged()
			if t.Failed() {
				return // Do not exercise concurrent mutations while maps still alias.
			}
			var workers sync.WaitGroup
			workers.Go(func() {
				for range 100 {
					nested["model"] = modelsdev.Entry{}
					delete(snapshot.Entries, "outer")
					snapshot.Entries["outer"] = map[string]modelsdev.Entry{"model": {}}
				}
			})
			workers.Go(func() {
				for range 100 {
					assertUnchanged()
				}
			})
			workers.Wait()
			assertUnchanged()
		})
	}
}
