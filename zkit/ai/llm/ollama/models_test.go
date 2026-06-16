package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
)

// /api/show responses in the wild can carry context_length under
// different keys depending on the model's architecture
// ("llama.context_length", "qwen2.context_length", ...). The probe
// must scan for any "<arch>.context_length" key rather than
// enumerating archs — the table tests pin that.
func TestContextWindowFor_ParsesModelInfo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "llama arch",
			body: map[string]any{
				"model_info": map[string]any{"llama.context_length": 131072},
			},
			want: 131072,
		},
		{
			name: "qwen arch",
			body: map[string]any{
				"model_info": map[string]any{"qwen2.context_length": 32768},
			},
			want: 32768,
		},
		{
			name: "details.num_ctx fallback (older servers)",
			body: map[string]any{
				"details": map[string]any{"num_ctx": 8192},
			},
			want: 8192,
		},
		{
			name: "top-level num_ctx fallback",
			body: map[string]any{
				"num_ctx": 16384,
			},
			want: 16384,
		},
		{
			name: "missing fields",
			body: map[string]any{
				"model_info": map[string]any{"llama.something_else": 99},
			},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/show" {
					t.Errorf("got path %q, want /api/show", r.URL.Path)
				}
				if r.Method != http.MethodPost {
					t.Errorf("got method %q, want POST", r.Method)
				}
				var req map[string]string
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if req["name"] != "test-model" {
					t.Errorf("got name=%q, want test-model", req["name"])
				}
				_ = json.NewEncoder(w).Encode(c.body)
			}))
			defer srv.Close()

			got := ollama.ContextWindowFor(t.Context(), srv.URL+"/v1", "test-model")
			if got != c.want {
				t.Errorf("ContextWindowFor() = %d, want %d", got, c.want)
			}
		})
	}
}

// Probe failures (server down, model unknown, non-2xx) must collapse
// to 0 so the caller's fallback chain stays in charge. A non-zero
// return on failure would silently override a known-good value.
func TestContextWindowFor_FailureModes(t *testing.T) {
	t.Parallel()
	t.Run("server unreachable", func(t *testing.T) {
		t.Parallel()
		// Short caller timeout — port 1 may take seconds to fail to
		// connect on some Linux configs. The probe respects the
		// passed-in context so a 200ms budget caps test latency.
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		if got := ollama.ContextWindowFor(ctx, "http://127.0.0.1:1/v1", "any"); got != 0 {
			t.Errorf("unreachable server should return 0, got %d", got)
		}
	})
	t.Run("404 from server", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if got := ollama.ContextWindowFor(t.Context(), srv.URL, "any"); got != 0 {
			t.Errorf("404 should return 0, got %d", got)
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{not json")
		}))
		defer srv.Close()
		if got := ollama.ContextWindowFor(t.Context(), srv.URL, "any"); got != 0 {
			t.Errorf("malformed JSON should return 0, got %d", got)
		}
	})
	t.Run("empty model name", func(t *testing.T) {
		t.Parallel()
		// No server call expected — empty name short-circuits.
		if got := ollama.ContextWindowFor(t.Context(), "http://x", ""); got != 0 {
			t.Errorf("empty model should return 0 without a probe, got %d", got)
		}
	})
	t.Run("empty baseURL", func(t *testing.T) {
		t.Parallel()
		if got := ollama.ContextWindowFor(t.Context(), "", "any"); got != 0 {
			t.Errorf("empty baseURL should return 0, got %d", got)
		}
	})
}

// baseURL with a /v1 suffix should be stripped before the /api/show
// call — Ollama's REST API lives at the root, the /v1 path is the
// OpenAI-compat surface only.
func TestContextWindowFor_StripsV1Suffix(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"num_ctx": 1024})
	}))
	defer srv.Close()

	// Pass with /v1 suffix — should still hit /api/show, not /v1/api/show.
	_ = ollama.ContextWindowFor(t.Context(), srv.URL+"/v1", "model")
	if gotPath != "/api/show" {
		t.Errorf("path = %q, want /api/show (v1 suffix should be stripped)", gotPath)
	}
	if !strings.HasSuffix(srv.URL+"/v1", "/v1") {
		t.Fatal("test setup: expected URL to end with /v1")
	}
}
