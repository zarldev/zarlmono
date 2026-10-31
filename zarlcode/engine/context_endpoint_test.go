package engine_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestContextBudgetUsesRegistryEndpointAndExplicitCap(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":8192}}`)
	}))
	t.Cleanup(server.Close)
	for _, windowCap := range []int{0, 4096} {
		t.Run(map[int]string{0: "live", 4096: "explicit"}[windowCap], func(t *testing.T) {
			t.Parallel()
			settings := newJudgeTestSettings(t)
			settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "llamacpp", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL + "/v1", ContextWindow: windowCap, Enabled: true, Builtin: true}}))
			want := 8192
			if windowCap > 0 {
				want = windowCap
			}
			if got := settings.ContextWindow(t.Context(), engine.ProviderSpec{Name: "llamacpp", Model: "fixture"}); got != want {
				t.Fatalf("got %d, want %d", got, want)
			}
		})
	}
}
