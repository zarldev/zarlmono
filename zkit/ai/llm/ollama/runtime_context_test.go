package ollama_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
)

func TestLoadedRuntimeContextOverridesArchitecturalCapacity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" || r.Method != http.MethodGet {
			t.Errorf("runtime probe fell through to architectural show: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"test-model:latest","context_length":4096}]}`))
	}))
	t.Cleanup(server.Close)
	if window := ollama.ContextWindowFor(t.Context(), server.URL+"/v1", "test-model"); window != 4096 {
		t.Fatalf("runtime window = %d, want 4096", window)
	}
}
