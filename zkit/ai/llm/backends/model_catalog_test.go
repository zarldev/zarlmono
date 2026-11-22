package backends_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func catalogTime() time.Time { return time.Date(2030, 4, 5, 6, 7, 8, 0, time.UTC) }

func catalogSurface(s llm.Support) llm.ModelSurface {
	return llm.ModelSurface{
		Support: s,
		Capabilities: llm.EffectiveCapabilities{
			Streaming: s, Tools: s, SystemMessages: s, JSONObject: s, JSONSchema: s,
			Thinking: s, ReasoningOutput: s, ReasoningReplay: s, PromptCaching: s,
		},
		Modalities: llm.DirectionalModalities{
			Input:  llm.ModalitySupport{Text: s, Image: s, Audio: s, Video: s},
			Output: llm.ModalitySupport{Text: s, Image: s, Audio: s, Video: s},
		},
	}
}

func catalogEvidence(surface llm.ModelSurface) []backends.ModelEvidence {
	return []backends.ModelEvidence{{
		Assertions: map[llm.ModelProtocol]llm.ModelSurface{llm.ModelProtocols.PROTOCOLRESPONSES: surface},
		Provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION, Snapshot: "standing"},
	}}
}

func catalogBinding() backends.ModelBinding {
	surface := catalogSurface(llm.Supports.SUPPORTSUPPORTED)
	return backends.ModelBinding{
		Route: " gateway/ Route:V1 ", RequestedModel: " UnknownOrg/Alias:latest ", Exhaustive: true,
		Client: catalogEvidence(surface),
		Candidates: []backends.ModelCandidate{{
			CanonicalModel: "Owner/Canonical:V1", ServingProvider: "serving org", Deployment: "deployment/123",
			RateCard: llm.RateCardReference{ID: "billing org", Revision: "r1", Entry: "specific SKU"},
			Model:    catalogEvidence(surface), DeploymentConstraints: catalogEvidence(surface), Gateway: catalogEvidence(surface),
		}},
	}
}

func resolvedSurface(result backends.ModelResolution) llm.ModelSurface {
	return result.Descriptor.Surfaces[llm.ModelProtocols.PROTOCOLRESPONSES]
}

func containsCatalogIssue(result backends.ModelResolution, kind backends.ModelResolutionIssueKind, layer backends.ModelEvidenceLayer, candidate int, field string) bool {
	for _, issue := range result.Issues {
		if issue.Kind == kind && issue.Layer == layer && issue.Candidate == candidate && issue.Field == field {
			return true
		}
	}
	return false
}

func TestModelCatalogRequiredLayers(t *testing.T) {
	registry := backends.NewRegistry()
	supported, unsupported := llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED
	tests := []struct {
		name   string
		change func(*backends.ModelBinding)
		want   llm.ModelSurface
	}{
		{name: "all required layers", change: func(*backends.ModelBinding) {}, want: catalogSurface(supported)},
		{name: "client denies", change: func(b *backends.ModelBinding) { b.Client = catalogEvidence(catalogSurface(unsupported)) }, want: catalogSurface(unsupported)},
		{name: "model denies", change: func(b *backends.ModelBinding) { b.Candidates[0].Model = catalogEvidence(catalogSurface(unsupported)) }, want: catalogSurface(unsupported)},
		{name: "deployment denies", change: func(b *backends.ModelBinding) {
			b.Candidates[0].DeploymentConstraints = catalogEvidence(catalogSurface(unsupported))
		}, want: catalogSurface(unsupported)},
		{name: "gateway denies", change: func(b *backends.ModelBinding) { b.Candidates[0].Gateway = catalogEvidence(catalogSurface(unsupported)) }, want: catalogSurface(unsupported)},
		{name: "gateway absent", change: func(b *backends.ModelBinding) { b.Candidates[0].Gateway = nil }},
		{name: "model absent", change: func(b *backends.ModelBinding) { b.Candidates[0].Model = nil }},
		{name: "deployment absent", change: func(b *backends.ModelBinding) { b.Candidates[0].DeploymentConstraints = nil }},
		{name: "client absent", change: func(b *backends.ModelBinding) { b.Client = nil }},
		{name: "direct gateway ignored", change: func(b *backends.ModelBinding) {
			b.Direct = true
			b.Candidates[0].Gateway = catalogEvidence(catalogSurface(unsupported))
		}, want: catalogSurface(supported)},
		{name: "policy cannot grant", change: func(b *backends.ModelBinding) {
			b.Client = nil
			b.Policy = catalogEvidence(catalogSurface(supported))
		}},
		{name: "policy denial", change: func(b *backends.ModelBinding) { b.Policy = catalogEvidence(catalogSurface(unsupported)) }, want: catalogSurface(unsupported)},
		{name: "unknown policy neutral", change: func(b *backends.ModelBinding) { b.Policy = catalogEvidence(llm.ModelSurface{}) }, want: catalogSurface(supported)},
		{name: "policy grant cannot erase denial", change: func(b *backends.ModelBinding) {
			b.Policy = append(catalogEvidence(catalogSurface(supported)), catalogEvidence(catalogSurface(unsupported))...)
		}, want: catalogSurface(unsupported)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := catalogBinding()
			test.change(&binding)
			result := registry.ResolveModelCached(binding, catalogTime())
			if got := resolvedSurface(result); got != test.want {
				t.Fatalf("surface = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestModelCatalogFreshness(t *testing.T) {
	at := catalogTime()
	tests := []struct {
		name       string
		provenance llm.MetadataProvenance
		want       llm.Support
		issue      backends.ModelResolutionIssueKind
	}{
		{name: "standing configuration", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION}, want: llm.Supports.SUPPORTSUPPORTED},
		{name: "nonexpiring configuration", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION, ObservedAt: at.Add(-time.Hour)}, want: llm.Supports.SUPPORTSUPPORTED},
		{name: "fresh catalogue", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACATALOGUE, ObservedAt: at.Add(-time.Hour), ExpiresAt: at.Add(time.Hour)}, want: llm.Supports.SUPPORTSUPPORTED},
		{name: "fresh discovery", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATADISCOVERY, ObservedAt: at, ExpiresAt: at.Add(time.Hour)}, want: llm.Supports.SUPPORTSUPPORTED},
		{name: "expired configuration", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION, ExpiresAt: at.Add(-time.Second)}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONSTALE},
		{name: "expiry inclusive", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACATALOGUE, ObservedAt: at.Add(-time.Hour), ExpiresAt: at}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONSTALE},
		{name: "undated discovery", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATADISCOVERY, ExpiresAt: at.Add(time.Hour)}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONUNDATED},
		{name: "missing catalogue expiry", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACATALOGUE, ObservedAt: at.Add(-time.Hour)}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONUNDATED},
		{name: "future discovery", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATADISCOVERY, ObservedAt: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONUNDATED},
		{name: "future configuration", provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION, ObservedAt: at.Add(time.Minute)}, issue: backends.ModelResolutionIssueKinds.RESOLUTIONUNDATED},
		{name: "unknown source", issue: backends.ModelResolutionIssueKinds.RESOLUTIONUNDATED},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, assertion := range []llm.Support{llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED} {
				binding := catalogBinding()
				binding.Candidates[0].Model = catalogEvidence(catalogSurface(assertion))
				binding.Candidates[0].Model[0].Provenance = test.provenance
				result := backends.NewRegistry().ResolveModelCached(binding, at)
				want := test.want
				if assertion == llm.Supports.SUPPORTUNSUPPORTED {
					want = assertion
				}
				if got := resolvedSurface(result); got != catalogSurface(want) {
					t.Fatalf("surface = %+v, want assertion %v", got, want)
				}
				if test.issue != (backends.ModelResolutionIssueKind{}) && !containsCatalogIssue(result, test.issue, backends.ModelEvidenceLayers.LAYERMODEL, 0, "provenance") {
					t.Fatalf("missing age issue %v: %+v", test.issue, result.Issues)
				}
				if test.issue == (backends.ModelResolutionIssueKind{}) && len(result.Issues) != 0 {
					t.Fatalf("fresh/standing evidence produced issues: %+v", result.Issues)
				}
			}
		})
	}
}

func TestModelCatalogConflictsAndCeilings(t *testing.T) {
	binding := catalogBinding()
	positive := catalogSurface(llm.Supports.SUPPORTSUPPORTED)
	positive.Limits = llm.ModelLimits{InputTokens: 100, OutputTokens: 20, ContextTokens: 120}
	negative := catalogSurface(llm.Supports.SUPPORTUNSUPPORTED)
	negative.Limits = llm.ModelLimits{InputTokens: 80, OutputTokens: 10, ContextTokens: 90}
	binding.Candidates[0].Model = append(catalogEvidence(positive), catalogEvidence(negative)...)
	binding.Candidates[0].Model[1].Provenance.Revision = "newer-but-not-preferred"
	result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
	want := llm.ModelSurface{Limits: negative.Limits}
	if resolvedSurface(result) != want {
		t.Fatalf("conflicting assertions = %+v, want %+v", resolvedSurface(result), want)
	}
	if len(result.Issues) != 18 {
		t.Fatalf("issues = %d, want one conflict per support field: %+v", len(result.Issues), result.Issues)
	}
	for _, issue := range result.Issues {
		if issue.Kind != backends.ModelResolutionIssueKinds.RESOLUTIONCONFLICT || issue.Layer != backends.ModelEvidenceLayers.LAYERMODEL || issue.Candidate != 0 || issue.Protocol != llm.ModelProtocols.PROTOCOLRESPONSES || len(issue.Evidence) != 2 {
			t.Fatalf("conflict lost claim identities: %+v", issue)
		}
	}
}

func TestModelCatalogCandidateIntersection(t *testing.T) {
	tests := []struct {
		name   string
		change func(*backends.ModelBinding)
		want   llm.Support
	}{
		{name: "homogeneous exhaustive", change: func(*backends.ModelBinding) {}, want: llm.Supports.SUPPORTSUPPORTED},
		{name: "nonexhaustive", change: func(b *backends.ModelBinding) { b.Exhaustive = false }},
		{name: "heterogeneous denial", change: func(b *backends.ModelBinding) {
			b.Candidates[1].Model = catalogEvidence(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED))
		}, want: llm.Supports.SUPPORTUNSUPPORTED},
		{name: "unknown second model", change: func(b *backends.ModelBinding) { b.Candidates[1].Model = nil }},
		{name: "incomplete still denies", change: func(b *backends.ModelBinding) {
			b.Exhaustive = false
			b.Candidates[1].Model = catalogEvidence(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED))
		}, want: llm.Supports.SUPPORTUNSUPPORTED},
		{name: "zero candidates", change: func(b *backends.ModelBinding) { b.Candidates = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := catalogBinding()
			second := catalogBinding().Candidates[0]
			binding.Candidates = append(binding.Candidates, second)
			test.change(&binding)
			result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
			if got := resolvedSurface(result); got != catalogSurface(test.want) {
				t.Fatalf("surface = %+v, want %v", got, test.want)
			}
			if len(binding.Candidates) == 0 || !binding.Exhaustive {
				if !containsCatalogIssue(result, backends.ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, backends.ModelEvidenceLayer{}, -1, "candidates") {
					t.Fatalf("missing candidate issue: %+v", result.Issues)
				}
			}
		})
	}
}

func setCatalogLimits(evidence []backends.ModelEvidence, limits llm.ModelLimits) {
	protocol := llm.ModelProtocols.PROTOCOLRESPONSES
	surface := evidence[0].Assertions[protocol]
	surface.Limits = limits
	evidence[0].Assertions[protocol] = surface
}

func TestModelCatalogIndependentLimits(t *testing.T) {
	tests := []struct {
		name   string
		change func(*backends.ModelBinding)
		want   llm.ModelLimits
	}{
		{name: "no arithmetic derivation", change: func(b *backends.ModelBinding) {
			setCatalogLimits(b.Candidates[0].Model, llm.ModelLimits{ContextTokens: 100, OutputTokens: 20})
		}, want: llm.ModelLimits{ContextTokens: 100, OutputTokens: 20}},
		{name: "layer minima and policy", change: func(b *backends.ModelBinding) {
			setCatalogLimits(b.Candidates[0].Model, llm.ModelLimits{ContextTokens: 200, InputTokens: 150, OutputTokens: 80})
			setCatalogLimits(b.Candidates[0].Gateway, llm.ModelLimits{ContextTokens: 100, OutputTokens: 50})
			b.Policy = catalogEvidence(llm.ModelSurface{Limits: llm.ModelLimits{InputTokens: 90, OutputTokens: 60}})
		}, want: llm.ModelLimits{ContextTokens: 100, InputTokens: 90, OutputTokens: 50}},
		{name: "policy cannot create bound", change: func(b *backends.ModelBinding) {
			b.Policy = catalogEvidence(llm.ModelSurface{Limits: llm.ModelLimits{ContextTokens: 10, InputTokens: 5, OutputTokens: 5}})
		}},
		{name: "configuration is an upper bound", change: func(b *backends.ModelBinding) {
			b.Candidates[0].Model = nil
			setCatalogLimits(b.Client, llm.ModelLimits{InputTokens: 75})
		}, want: llm.ModelLimits{InputTokens: 75}},
		{name: "restriction survives expiry", change: func(b *backends.ModelBinding) {
			setCatalogLimits(b.Candidates[0].Model, llm.ModelLimits{OutputTokens: 10})
			b.Candidates[0].Model[0].Provenance.ExpiresAt = catalogTime().Add(-time.Hour)
		}, want: llm.ModelLimits{OutputTokens: 10}},
		{name: "candidate unknown dimensions", change: func(b *backends.ModelBinding) {
			setCatalogLimits(b.Candidates[0].Model, llm.ModelLimits{ContextTokens: 100, InputTokens: 90, OutputTokens: 20})
			second := catalogBinding().Candidates[0]
			setCatalogLimits(second.Model, llm.ModelLimits{ContextTokens: 80, OutputTokens: 30})
			b.Candidates = append(b.Candidates, second)
		}, want: llm.ModelLimits{ContextTokens: 80, OutputTokens: 20}},
		{name: "incomplete candidates no bound", change: func(b *backends.ModelBinding) {
			setCatalogLimits(b.Candidates[0].Model, llm.ModelLimits{ContextTokens: 100, InputTokens: 90, OutputTokens: 20})
			b.Exhaustive = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := catalogBinding()
			test.change(&binding)
			result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
			if got := resolvedSurface(result).Limits; got != test.want {
				t.Fatalf("limits = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestModelCatalogOpaqueIdentityConsensus(t *testing.T) {
	registry := backends.NewRegistry()
	binding := catalogBinding()
	result := registry.ResolveModelCached(binding, catalogTime())
	if result.Descriptor.Route != binding.Route || result.Descriptor.RequestedModel != binding.RequestedModel || result.Descriptor.CanonicalModel != binding.Candidates[0].CanonicalModel || result.Descriptor.ServingProvider != binding.Candidates[0].ServingProvider || result.Descriptor.Deployment != binding.Candidates[0].Deployment || result.Descriptor.RateCard != binding.Candidates[0].RateCard {
		t.Fatalf("opaque identities were changed: %+v", result.Descriptor)
	}
	second := catalogBinding().Candidates[0]
	binding.Candidates = append(binding.Candidates, second)
	if got := registry.ResolveModelCached(binding, catalogTime()).Descriptor; got.CanonicalModel != result.Descriptor.CanonicalModel || got.RateCard != result.Descriptor.RateCard {
		t.Fatalf("homogeneous candidates lost identity: %+v", got)
	}
	binding.Candidates[1].CanonicalModel = "different model"
	binding.Candidates[1].ServingProvider = "different provider"
	binding.Candidates[1].Deployment = "different deployment"
	binding.Candidates[1].RateCard.Entry = "different SKU"
	for _, exhaustive := range []bool{true, false} {
		binding.Exhaustive = exhaustive
		got := registry.ResolveModelCached(binding, catalogTime()).Descriptor
		if got.Route != binding.Route || got.RequestedModel != binding.RequestedModel || got.CanonicalModel != "" || got.ServingProvider != "" || got.Deployment != "" || got.RateCard != (llm.RateCardReference{}) {
			t.Fatalf("heterogeneous/incomplete identity guessed: %+v", got)
		}
	}
}

// catalogCountingCache detects backing-store reads; embedded operations preserve
// the real cache's behavior during explicit source construction and warming.
type catalogCountingCache struct {
	cache.Cache[string, modelsdev.Snapshot]
	reads atomic.Int64
}

func (c *catalogCountingCache) Get(ctx context.Context, key string) (modelsdev.Snapshot, error) {
	c.reads.Add(1)
	return c.Cache.Get(ctx, key)
}

func TestModelCatalogExactCachedEvidence(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected discovery", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	store := &catalogCountingCache{Cache: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	// Warming uses the source's clock; all resolver decisions use offsets from
	// this captured observation, with no sleeps or time-dependent assertions.
	observed := time.Now().UTC()
	if err := store.Set(t.Context(), "models.dev", modelsdev.Snapshot{
		Schema: 4, FetchedAt: observed, Revision: "catalogue revision",
		Entries: map[string]map[string]modelsdev.Entry{"google": {"Owner/Model:V1": {Evidence: modelsdev.Evidence{
			Tools: llm.Supports.SUPPORTSUPPORTED, Reasoning: llm.Supports.SUPPORTSUPPORTED,
			Modalities: modelsdev.DirectionalModalities{Input: modelsdev.ModalityEvidence{Image: llm.Supports.SUPPORTSUPPORTED}, Output: modelsdev.ModalityEvidence{Audio: llm.Supports.SUPPORTSUPPORTED}},
			Limits:     llm.ModelLimits{InputTokens: 80, OutputTokens: 20},
		}}}},
	}); err != nil {
		t.Fatal(err)
	}
	source := modelsdev.New(store, modelsdev.WithTTL(time.Hour), modelsdev.WithBaseURL(server.URL))
	registry := backends.NewRegistry(backends.WithModelsDevSource(source))
	binding := catalogBinding()
	binding.Candidates[0].CatalogueProvider = "google"
	binding.Candidates[0].CanonicalModel = "Owner/Model:V1"
	binding.Candidates[0].Model = catalogEvidence(llm.ModelSurface{Support: llm.Supports.SUPPORTSUPPORTED})
	if got := resolvedSurface(registry.ResolveModelCached(binding, observed)); got.Capabilities.Tools != llm.Supports.SUPPORTUNKNOWN || store.reads.Load() != 0 || requests.Load() != 0 {
		t.Fatalf("unwarmed source read external data: %+v", got)
	}
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	reads := store.reads.Load()
	tests := []struct {
		name     string
		provider string
		model    llm.ModelID
		at       time.Time
		found    bool
	}{
		{name: "exact", provider: "google", model: "Owner/Model:V1", at: observed, found: true},
		{name: "case mismatch", provider: "google", model: "owner/model:v1", at: observed},
		{name: "no basename inference", provider: "google", model: "Model:V1", at: observed},
		{name: "no provider alias", provider: "gemini", model: "Owner/Model:V1", at: observed},
		{name: "no model identity", provider: "google", at: observed},
		{name: "no provider identity", model: "Owner/Model:V1", at: observed},
		{name: "no trim", provider: "google", model: " Owner/Model:V1 ", at: observed},
		{name: "stale cached only", provider: "google", model: "Owner/Model:V1", at: observed.Add(2 * time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding.Candidates[0].CatalogueProvider = test.provider
			binding.Candidates[0].CanonicalModel = test.model
			result := registry.ResolveModelCached(binding, test.at)
			surface := resolvedSurface(result)
			want := llm.Supports.SUPPORTUNKNOWN
			if test.found {
				want = llm.Supports.SUPPORTSUPPORTED
			}
			if surface.Support != llm.Supports.SUPPORTSUPPORTED || surface.Capabilities.Tools != want || surface.Modalities.Input.Image != want || surface.Modalities.Output.Audio != want {
				t.Fatalf("exact catalogue resolution = %+v, found=%v", surface, test.found)
			}
			if surface.Capabilities.Streaming != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.SystemMessages != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.Thinking != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.ReasoningOutput != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.ReasoningReplay != llm.Supports.SUPPORTUNKNOWN {
				t.Fatalf("catalogue fabricated protocol/adapter features: %+v", surface.Capabilities)
			}
			if store.reads.Load() != reads || requests.Load() != 0 {
				t.Fatal("cached resolver performed I/O")
			}
		})
	}
	// A populated catalogue must not establish any protocol by itself.
	binding = catalogBinding()
	binding.Candidates[0].CanonicalModel = "Owner/Model:V1"
	binding.Candidates[0].CatalogueProvider = "google"
	binding.Client, binding.Candidates[0].Model, binding.Candidates[0].DeploymentConstraints, binding.Candidates[0].Gateway = nil, nil, nil, nil
	result := registry.ResolveModelCached(binding, observed)
	if len(result.Descriptor.Surfaces) != 0 || !containsCatalogIssue(result, backends.ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, backends.ModelEvidenceLayer{}, -1, "protocols") {
		t.Fatalf("catalogue invented a protocol: %+v", result)
	}
}

func TestModelCatalogSnapshotsAndDeterminism(t *testing.T) {
	registry := backends.NewRegistry()
	binding := catalogBinding()
	binding.Candidates[0].Model = append(binding.Candidates[0].Model, catalogEvidence(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED))...)
	for _, layer := range [][]backends.ModelEvidence{binding.Client, binding.Candidates[0].Model, binding.Candidates[0].DeploymentConstraints, binding.Candidates[0].Gateway} {
		for _, evidence := range layer {
			evidence.Assertions[llm.ModelProtocols.PROTOCOLMESSAGES] = evidence.Assertions[llm.ModelProtocols.PROTOCOLRESPONSES]
		}
	}
	first := registry.ResolveModelCached(binding, catalogTime())
	for range 50 {
		if got := registry.ResolveModelCached(binding, catalogTime()); !reflect.DeepEqual(got, first) {
			t.Fatalf("nondeterministic result:\n%+v\n%+v", got, first)
		}
	}
	retained := registry.ResolveModelCached(binding, catalogTime())
	binding.Candidates[0].Model[0].Provenance.Snapshot = "caller mutation"
	binding.Candidates[0].Model[0].Assertions[llm.ModelProtocols.PROTOCOLRESPONSES] = llm.ModelSurface{}
	if !reflect.DeepEqual(first, retained) {
		t.Fatal("input mutation changed retained output")
	}
	first.Descriptor.Surfaces[llm.ModelProtocols.PROTOCOLRESPONSES] = catalogSurface(llm.Supports.SUPPORTSUPPORTED)
	first.Descriptor.Evidence[0].Snapshot = "output mutation"
	first.Issues[0].Evidence[0].Snapshot = "issue mutation"
	if retained.Descriptor.Evidence[0].Snapshot == "output mutation" || retained.Issues[0].Evidence[0].Snapshot == "issue mutation" || resolvedSurface(retained).Support == llm.Supports.SUPPORTSUPPORTED {
		t.Fatal("separate snapshots share mutable output")
	}
	if first.Issues[1].Evidence[0].Snapshot == "issue mutation" || first.Descriptor.Evidence[1].Snapshot == "issue mutation" || binding.Candidates[0].Model[0].Provenance.Snapshot != "caller mutation" {
		t.Fatal("issues share provenance slices")
	}
}

func TestModelCatalogIndependentClaims(t *testing.T) {
	s := llm.Supports.SUPPORTSUPPORTED
	tests := []struct {
		name    string
		surface llm.ModelSurface
	}{
		{name: "protocol", surface: llm.ModelSurface{Support: s}},
		{name: "streaming", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{Streaming: s}}},
		{name: "tools", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{Tools: s}}},
		{name: "system", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{SystemMessages: s}}},
		{name: "json object", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{JSONObject: s}}},
		{name: "json schema", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{JSONSchema: s}}},
		{name: "thinking", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{Thinking: s}}},
		{name: "reasoning output", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{ReasoningOutput: s}}},
		{name: "reasoning replay", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{ReasoningReplay: s}}},
		{name: "prompt caching", surface: llm.ModelSurface{Support: s, Capabilities: llm.EffectiveCapabilities{PromptCaching: s}}},
		{name: "input text", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Input: llm.ModalitySupport{Text: s}}}},
		{name: "input image", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Input: llm.ModalitySupport{Image: s}}}},
		{name: "input audio", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Input: llm.ModalitySupport{Audio: s}}}},
		{name: "input video", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Input: llm.ModalitySupport{Video: s}}}},
		{name: "output text", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Output: llm.ModalitySupport{Text: s}}}},
		{name: "output image", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Output: llm.ModalitySupport{Image: s}}}},
		{name: "output audio", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Output: llm.ModalitySupport{Audio: s}}}},
		{name: "output video", surface: llm.ModelSurface{Support: s, Modalities: llm.DirectionalModalities{Output: llm.ModalitySupport{Video: s}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := catalogBinding()
			binding.Client = catalogEvidence(test.surface)
			binding.Candidates[0].Model = catalogEvidence(test.surface)
			binding.Candidates[0].DeploymentConstraints = catalogEvidence(test.surface)
			binding.Candidates[0].Gateway = catalogEvidence(test.surface)
			if got := resolvedSurface(backends.NewRegistry().ResolveModelCached(binding, catalogTime())); got != test.surface {
				t.Fatalf("independent claim changed: %+v, want %+v", got, test.surface)
			}
		})
	}
}

// Feature evidence stays fresh and positive while protocol evidence varies.
// No effective feature may survive a missing or denied inference path.
func TestModelCatalogProtocolGatesFeatures(t *testing.T) {
	supported, unsupported := llm.Supports.SUPPORTSUPPORTED, llm.Supports.SUPPORTUNSUPPORTED
	protocol := llm.ModelProtocols.PROTOCOLRESPONSES
	setSupport := func(evidence []backends.ModelEvidence, support llm.Support) {
		surface := evidence[0].Assertions[protocol]
		surface.Support = support
		evidence[0].Assertions[protocol] = surface
	}
	protocolEvidence := func(provenance llm.MetadataProvenance) []backends.ModelEvidence {
		evidence := catalogEvidence(llm.ModelSurface{Support: supported})
		evidence[0].Provenance = provenance
		return evidence
	}
	agedProtocol := func(provenance llm.MetadataProvenance) func(*backends.ModelBinding) {
		return func(b *backends.ModelBinding) {
			setSupport(b.Candidates[0].Model, llm.Supports.SUPPORTUNKNOWN)
			b.Candidates[0].Model = append(b.Candidates[0].Model, protocolEvidence(provenance)...)
		}
	}
	tests := []struct {
		name   string
		change func(*backends.ModelBinding)
		want   llm.Support
	}{
		{name: "supported protocol", change: func(*backends.ModelBinding) {}, want: supported},
		{name: "unknown model protocol", change: func(b *backends.ModelBinding) { setSupport(b.Candidates[0].Model, llm.Supports.SUPPORTUNKNOWN) }},
		{name: "unknown deployment protocol", change: func(b *backends.ModelBinding) {
			setSupport(b.Candidates[0].DeploymentConstraints, llm.Supports.SUPPORTUNKNOWN)
		}},
		{name: "unknown gateway protocol", change: func(b *backends.ModelBinding) { setSupport(b.Candidates[0].Gateway, llm.Supports.SUPPORTUNKNOWN) }},
		{name: "unknown client protocol", change: func(b *backends.ModelBinding) { setSupport(b.Client, llm.Supports.SUPPORTUNKNOWN) }},
		{name: "explicit client protocol denial", change: func(b *backends.ModelBinding) { setSupport(b.Client, unsupported) }, want: unsupported},
		{name: "policy protocol denial", change: func(b *backends.ModelBinding) { b.Policy = catalogEvidence(llm.ModelSurface{Support: unsupported}) }, want: unsupported},
		{name: "protocol conflict", change: func(b *backends.ModelBinding) {
			b.Client = append(b.Client, catalogEvidence(llm.ModelSurface{Support: unsupported})...)
		}},
		{name: "stale protocol", change: agedProtocol(llm.MetadataProvenance{
			Source: llm.MetadataSources.METADATADISCOVERY, ObservedAt: catalogTime().Add(-time.Hour), ExpiresAt: catalogTime(),
		})},
		{name: "future protocol", change: agedProtocol(llm.MetadataProvenance{
			Source: llm.MetadataSources.METADATADISCOVERY, ObservedAt: catalogTime().Add(time.Minute), ExpiresAt: catalogTime().Add(time.Hour),
		})},
		{name: "undated protocol", change: agedProtocol(llm.MetadataProvenance{Source: llm.MetadataSources.METADATADISCOVERY})},
	}
	modes := []struct {
		name       string
		second     bool
		reverse    bool
		exhaustive bool
	}{
		{name: "single", exhaustive: true},
		{name: "first candidate", second: true, exhaustive: true},
		{name: "last candidate", second: true, reverse: true, exhaustive: true},
		{name: "incomplete candidates", second: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range modes {
				t.Run(mode.name, func(t *testing.T) {
					binding := catalogBinding()
					test.change(&binding)
					if mode.second {
						binding.Candidates = append(binding.Candidates, catalogBinding().Candidates[0])
					}
					limits := llm.ModelLimits{InputTokens: 80, OutputTokens: 20, ContextTokens: 100}
					for _, candidate := range binding.Candidates {
						setCatalogLimits(candidate.Model, limits)
					}
					if mode.reverse {
						binding.Candidates[0], binding.Candidates[1] = binding.Candidates[1], binding.Candidates[0]
					}
					binding.Exhaustive = mode.exhaustive
					wantSupport := test.want
					if !mode.exhaustive && wantSupport == supported {
						wantSupport = llm.Supports.SUPPORTUNKNOWN
					}
					want := catalogSurface(wantSupport)
					if mode.exhaustive {
						want.Limits = limits
					}
					got := resolvedSurface(backends.NewRegistry().ResolveModelCached(binding, catalogTime()))
					if got != want {
						t.Fatalf("protocol-gated surface = %+v, want %+v", got, want)
					}
				})
			}
		})
	}
}

func TestModelCatalogProtocolGatePreservesFeatureDenials(t *testing.T) {
	binding := catalogBinding()
	denied := catalogSurface(llm.Supports.SUPPORTUNSUPPORTED)
	denied.Support = llm.Supports.SUPPORTUNKNOWN
	binding.Client = catalogEvidence(denied)
	if got := resolvedSurface(backends.NewRegistry().ResolveModelCached(binding, catalogTime())); got != denied {
		t.Fatalf("unknown protocol erased feature restrictions: %+v, want %+v", got, denied)
	}
}

func TestModelCatalogSeparateProtocols(t *testing.T) {
	binding := catalogBinding()
	binding.Candidates = append(binding.Candidates, catalogBinding().Candidates[0])
	binding.Candidates[1].Model = []backends.ModelEvidence{{
		Provenance: llm.MetadataProvenance{Source: llm.MetadataSources.METADATACONFIGURATION},
		Assertions: map[llm.ModelProtocol]llm.ModelSurface{llm.ModelProtocols.PROTOCOLMESSAGES: catalogSurface(llm.Supports.SUPPORTSUPPORTED)},
	}}
	result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
	if len(result.Descriptor.Surfaces) != 2 {
		t.Fatalf("protocol union = %+v", result.Descriptor.Surfaces)
	}
	for protocol, surface := range result.Descriptor.Surfaces {
		if surface != (llm.ModelSurface{}) {
			t.Fatalf("protocol %v borrowed other protocol evidence: %+v", protocol, surface)
		}
	}
	if !containsCatalogIssue(result, backends.ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, backends.ModelEvidenceLayers.LAYERCLIENT, -1, "support") {
		t.Fatalf("missing binding-wide incomplete issue: %+v", result.Issues)
	}
}

func TestModelCatalogDenialSurvivesOtherLayerConflict(t *testing.T) {
	binding := catalogBinding()
	binding.Candidates[0].Model = append(binding.Candidates[0].Model, catalogEvidence(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED))...)
	binding.Client = catalogEvidence(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED))
	result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
	if resolvedSurface(result) != catalogSurface(llm.Supports.SUPPORTUNSUPPORTED) || !containsCatalogIssue(result, backends.ModelResolutionIssueKinds.RESOLUTIONCONFLICT, backends.ModelEvidenceLayers.LAYERMODEL, 0, "tools") {
		t.Fatalf("conflict erased independent restriction or provenance: %+v", result)
	}
}

func TestModelCatalogIdentityFieldsRemainIndependent(t *testing.T) {
	binding := catalogBinding()
	binding.Candidates = append(binding.Candidates, catalogBinding().Candidates[0])
	binding.Candidates[1].Deployment = "different deployment"
	binding.Candidates[1].RateCard = llm.RateCardReference{}
	result := backends.NewRegistry().ResolveModelCached(binding, catalogTime())
	if result.Descriptor.CanonicalModel != binding.Candidates[0].CanonicalModel || result.Descriptor.ServingProvider != binding.Candidates[0].ServingProvider || result.Descriptor.Deployment != "" || result.Descriptor.RateCard != (llm.RateCardReference{}) {
		t.Fatalf("independent identity consensus = %+v", result.Descriptor)
	}
	binding.Exhaustive = false
	result = backends.NewRegistry().ResolveModelCached(binding, catalogTime())
	if result.Descriptor.CanonicalModel != "" || result.Descriptor.ServingProvider != "" {
		t.Fatalf("incomplete homogeneous identity guessed: %+v", result.Descriptor)
	}
}

func TestModelCatalogEmptyBinding(t *testing.T) {
	result := backends.NewRegistry().ResolveModelCached(backends.ModelBinding{}, catalogTime())
	if len(result.Descriptor.Surfaces) != 0 || !containsCatalogIssue(result, backends.ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, backends.ModelEvidenceLayer{}, -1, "candidates") {
		t.Fatalf("empty binding fabricated metadata: %+v", result)
	}
}

func TestModelCatalogConcurrentReadOnly(t *testing.T) {
	registry := backends.NewRegistry()
	binding := catalogBinding()
	want := registry.ResolveModelCached(binding, catalogTime())
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				if got := registry.ResolveModelCached(binding, catalogTime()); !reflect.DeepEqual(got, want) {
					t.Error("concurrent resolution changed immutable input or result")
				}
			}
		})
	}
	workers.Wait()
}

func TestModelCatalogLegacyProjectionRequiresProtocol(t *testing.T) {
	all := llm.ModelCapabilities{
		SupportsStreaming: true, SupportsVision: true, SupportsVideo: true,
		SupportsTools: true, SupportsSystem: true, SupportsThinking: true,
	}
	for _, protocolSupport := range []llm.Support{llm.Supports.SUPPORTUNKNOWN, llm.Supports.SUPPORTUNSUPPORTED, llm.Supports.SUPPORTSUPPORTED} {
		t.Run(protocolSupport.String(), func(t *testing.T) {
			// Callers may construct surfaces independently of the resolver.
			surface := catalogSurface(llm.Supports.SUPPORTSUPPORTED)
			surface.Support = protocolSupport
			want := llm.ModelCapabilities{}
			if protocolSupport == llm.Supports.SUPPORTSUPPORTED {
				want = all
			}
			if got := backends.LegacyCapabilities(surface); got != want {
				t.Fatalf("legacy projection = %+v, want %+v", got, want)
			}
		})
	}
}

func TestModelCatalogLegacyProjectionAndIsolation(t *testing.T) {
	if got := backends.LegacyCapabilities(llm.ModelSurface{}); got != (llm.ModelCapabilities{}) {
		t.Fatalf("unknown capabilities granted legacy defaults: %+v", got)
	}
	if got := backends.LegacyCapabilities(catalogSurface(llm.Supports.SUPPORTUNSUPPORTED)); got != (llm.ModelCapabilities{}) {
		t.Fatalf("denied capabilities projected true: %+v", got)
	}
	if got := backends.LegacyCapabilities(catalogSurface(llm.Supports.SUPPORTSUPPORTED)); got != (llm.ModelCapabilities{SupportsStreaming: true, SupportsVision: true, SupportsVideo: true, SupportsTools: true, SupportsSystem: true, SupportsThinking: true}) {
		t.Fatalf("supported projection lost features: %+v", got)
	}
	outputOnly := llm.ModelSurface{Support: llm.Supports.SUPPORTSUPPORTED, Modalities: llm.DirectionalModalities{Output: llm.ModalitySupport{Image: llm.Supports.SUPPORTSUPPORTED, Video: llm.Supports.SUPPORTSUPPORTED}}}
	if got := backends.LegacyCapabilities(outputOnly); got.SupportsVision || got.SupportsVideo {
		t.Fatalf("output generation inferred input support: %+v", got)
	}
	registry := backends.NewRegistry()
	legacy := registry.Capabilities("openai", "gpt-4o")
	binding := catalogBinding()
	binding.Route, binding.RequestedModel = "openai", "gpt-4o"
	binding.Candidates[0].Model = nil
	if got := resolvedSurface(registry.ResolveModelCached(binding, catalogTime())); got != (llm.ModelSurface{}) {
		t.Fatalf("new resolver consulted legacy intrinsic metadata: %+v", got)
	}
	if got := registry.Capabilities("openai", "gpt-4o"); got != legacy {
		t.Fatalf("new resolver changed legacy capabilities: %+v, want %+v", got, legacy)
	}
}

func TestModelCatalogWarmFactsNeverFillRequiredLayers(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"native":{"models":{"canonical":{"tool_call":true,"reasoning":true,"modalities":{"input":["image","pdf"],"output":["audio"]},"limit":{"input":80,"output":20},"cost":{"input":0,"output":3}}}}}`))
	}))
	t.Cleanup(server.Close)
	store := &catalogCountingCache{Cache: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	source := modelsdev.New(store, modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(time.Hour))
	if err := source.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	native, ok := source.LookupModelCached("native", "canonical")
	if !ok {
		t.Fatal("missing native catalogue")
	}
	at := native.Provenance.ObservedAt
	registry := backends.NewRegistry(backends.WithModelsDevSource(source))
	reads, fetches := store.reads.Load(), requests.Load()
	supported := llm.Supports.SUPPORTSUPPORTED
	protocolOnly := llm.ModelSurface{Support: supported}
	tests := []struct {
		name     string
		change   func(*backends.ModelBinding)
		protocol llm.Support
		features llm.Support
	}{
		{name: "complete required layers", change: func(*backends.ModelBinding) {}, protocol: supported, features: supported},
		{name: "client missing", change: func(b *backends.ModelBinding) { b.Client = nil }},
		{name: "deployment missing", change: func(b *backends.ModelBinding) { b.Candidates[0].DeploymentConstraints = nil }},
		{name: "protocol-only client", change: func(b *backends.ModelBinding) { b.Client = catalogEvidence(protocolOnly) }, protocol: supported},
		{name: "protocol-only deployment", change: func(b *backends.ModelBinding) { b.Candidates[0].DeploymentConstraints = catalogEvidence(protocolOnly) }, protocol: supported},
		{name: "protocol-only all layers", change: func(b *backends.ModelBinding) {
			b.Client = catalogEvidence(protocolOnly)
			b.Candidates[0].DeploymentConstraints = catalogEvidence(protocolOnly)
			b.Candidates[0].Gateway = catalogEvidence(protocolOnly)
		}, protocol: supported},
		{name: "catalogue without model protocol", change: func(b *backends.ModelBinding) { b.Candidates[0].Model = nil }},
		{name: "nonexhaustive", change: func(b *backends.ModelBinding) { b.Exhaustive = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := catalogBinding()
			binding.Candidates[0].CanonicalModel = "canonical"
			binding.Candidates[0].CatalogueProvider = "native"
			binding.Candidates[0].Model = catalogEvidence(protocolOnly)
			binding.Candidates[0].RateCard = llm.RateCardReference{}
			test.change(&binding)
			result := registry.ResolveModelCached(binding, at)
			surface := resolvedSurface(result)
			if surface.Support != test.protocol || surface.Capabilities.Tools != test.features || surface.Modalities.Input.Image != test.features || surface.Modalities.Output.Audio != test.features {
				t.Fatalf("required-layer intersection = %+v", surface)
			}
			if surface.Capabilities.Thinking != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.Streaming != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.ReasoningOutput != llm.Supports.SUPPORTUNKNOWN || surface.Capabilities.ReasoningReplay != llm.Supports.SUPPORTUNKNOWN {
				t.Fatalf("native facts fabricated protocol features: %+v", surface)
			}
			limits := llm.ModelLimits{InputTokens: 80, OutputTokens: 20}
			if !binding.Exhaustive {
				limits = llm.ModelLimits{}
			}
			if surface.Limits != limits {
				t.Fatalf("independent bounds = %+v, want %+v", surface.Limits, limits)
			}
			if result.Descriptor.RequestedModel != binding.RequestedModel || result.Descriptor.Route != binding.Route || result.Descriptor.RateCard != (llm.RateCardReference{}) {
				t.Fatalf("native catalogue changed alias or billing identity: %+v", result.Descriptor)
			}
			if store.reads.Load() != reads || requests.Load() != fetches {
				t.Fatal("resolver performed I/O")
			}
		})
	}
}
