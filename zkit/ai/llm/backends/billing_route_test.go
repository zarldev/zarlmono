package backends_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestSharedIntrinsicCatalogCannotInventRouteBilling(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"google":{"models":[{"id":"gemini-3.8-flash","cost":{"input":0.01,"output":0.01}}]},"deepseek":{"models":[{"id":"deepseek-flash","cost":{"input":0.15,"output":0.6}}]}}`)
	}))
	t.Cleanup(server.Close)
	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL))
	r := backends.NewRegistry(backends.WithModelsDevSource(source))
	// Populate actual shared metadata before checking cached and live pricing.
	_ = r.ResolveCapabilities(t.Context(), "google-vertex", "gemini-3.8-flash")
	for _, cached := range []bool{false, true} {
		var in, out float64
		var ok bool
		if cached {
			in, out, ok = r.ResolveCostCached("google-vertex", "gemini-3.8-flash")
		} else {
			in, out, ok = r.ResolveCost(t.Context(), "google-vertex", "gemini-3.8-flash")
		}
		if ok || in != 0 || out != 0 {
			t.Fatal("Vertex inherited Gemini tariff")
		}
	}
	if _, _, ok := r.ResolveCostForPrompt(t.Context(), "google-vertex", "gemini-3.8-flash", 500000); ok {
		t.Fatal("prompt billing inherited Gemini tariff")
	}
	in, out, ok := r.ResolveCost(t.Context(), "deepseek", "deepseek-flash")
	if !ok || in != .00030 || out != .00120 {
		t.Fatalf("offpeak catalog defeated conservative pricing: %v %v %v", in, out, ok)
	}
	for _, resolve := range []func() (float64, float64, bool){
		func() (float64, float64, bool) { return r.ResolveCost(t.Context(), "gemini", "gemini-3.8-flash") },
		func() (float64, float64, bool) { return r.ResolveCostCached("gemini", "gemini-3.8-flash") },
		func() (float64, float64, bool) {
			return r.ResolveCostForPrompt(t.Context(), "gemini", "gemini-3.8-flash", 500000)
		},
	} {
		input, output, known := resolve()
		if !known || input != .00150 || output != .00750 {
			t.Fatalf("generic catalogue replaced qualified Google estimate: %v/%v/%v", input, output, known)
		}
	}
	override := backends.NewRegistry(backends.WithModelsDevSource(source), backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "vertex-custom", AdapterType: backends.AdapterTypes.GOOGLEVERTEX, InputCostPerMTok: 7, OutputCostPerMTok: 9, Enabled: true}}))
	for _, resolve := range []func() (float64, float64, bool){
		func() (float64, float64, bool) {
			return override.ResolveCost(t.Context(), "vertex-custom", "gemini-3.8-flash")
		},
		func() (float64, float64, bool) {
			return override.ResolveCostCached("vertex-custom", "gemini-3.8-flash")
		},
		func() (float64, float64, bool) {
			return override.ResolveCostForPrompt(t.Context(), "vertex-custom", "gemini-3.8-flash", 500000)
		},
	} {
		input, output, known := resolve()
		if !known || input != .007 || output != .009 {
			t.Fatal("explicit Vertex rates did not win")
		}
	}
}
