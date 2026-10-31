package backends_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestContextProbeUsesConstructedRegistryEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":8192}}`)
	}))
	t.Cleanup(server.Close)
	registry := backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "llamacpp", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL + "/v1", Enabled: true, Builtin: true}}))
	if got := registry.ResolveContextWindow(t.Context(), "llamacpp", "", "fixture"); got != 8192 {
		t.Fatalf("got %d, want live registry endpoint 8192", got)
	}
}

func TestGoogleContextLimitsAreModelSpecific(t *testing.T) {
	t.Parallel()
	registry := backends.NewRegistry()
	for _, tc := range []struct {
		model  string
		window int
	}{{"gemini-2.5-flash", 1048576}, {"gemini-2.5-pro", 1048576}, {"gemini-1.5-pro", 2097152}, {"gemini-fictional", 0}} {
		for _, route := range []string{"gemini", "google-vertex"} {
			t.Run(route+"/"+tc.model, func(t *testing.T) {
				t.Parallel()
				if got := registry.ContextWindow(route, tc.model); got != tc.window {
					t.Fatalf("got %d, want %d", got, tc.window)
				}
			})
		}
	}
}
