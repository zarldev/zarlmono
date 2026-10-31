package backends_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestGoogleCustomRegistryDiscoveryAndGenerationShareVersionedPrefix(t *testing.T) {
	const apiKey = "google-discovery-canary"

	var discoveryRequests atomic.Int32
	var generationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Goog-Api-Key"); got != apiKey {
			t.Errorf("%s X-Goog-Api-Key = %q, want canary", r.Method, got)
		}

		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/gemini/v1beta/models" {
				t.Errorf("discovery path = %q, want /gemini/v1beta/models", r.URL.Path)
			}
			request := discoveryRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			var response any
			switch request {
			case 1:
				if token := r.URL.Query().Get("pageToken"); token != "" {
					t.Errorf("first page token = %q, want empty", token)
				}
				response = map[string]any{
					"models": []map[string]any{
						{"name": "models/gemini-chat", "supportedGenerationMethods": []string{"generateContent", "countTokens"}},
						{"name": "models/gemini-live", "supportedGenerationMethods": []string{"bidiGenerateContent"}},
					},
					"nextPageToken": "page-two",
				}
			case 2:
				if token := r.URL.Query().Get("pageToken"); token != "page-two" {
					t.Errorf("second page token = %q, want page-two", token)
				}
				response = map[string]any{"models": []map[string]any{
					{"name": "models/gemini-chat", "supportedGenerationMethods": []string{"generateContent"}},
					{"name": "models/gemini-actions", "supportedActions": []string{"generateContent"}},
				}}
			default:
				t.Errorf("unexpected discovery request %d", request)
				response = map[string]any{"models": []any{}}
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Errorf("encode discovery response: %v", err)
			}
		case http.MethodPost:
			generationRequests.Add(1)
			if r.URL.Path != "/gemini/v1beta/models/gemini-chat:streamGenerateContent" {
				t.Errorf("generation path = %q, want versioned prefixed stream endpoint", r.URL.Path)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
		default:
			t.Errorf("method = %q, want GET or POST", r.Method)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	registry := googleTestRegistry(server.URL+"/gemini", apiKey)
	models, err := registry.FetchModels(t.Context(), "gemini-test")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	want := []string{"gemini-chat", "gemini-actions"}
	if !slices.Equal(models, want) {
		t.Fatalf("models = %v, want %v", models, want)
	}

	provider, err := registry.BuildWithConfig(t.Context(), "gemini-test", backends.BuildConfig{})
	if err != nil {
		t.Fatalf("BuildWithConfig: %v", err)
	}
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	if got := discoveryRequests.Load(); got != 2 {
		t.Errorf("discovery requests = %d, want 2", got)
	}
	if got := generationRequests.Load(); got != 1 {
		t.Errorf("generation requests = %d, want 1", got)
	}
}

func TestGoogleDiscoveryRejectsCrossOriginRedirectWithoutForwardingKey(t *testing.T) {
	const apiKey = "google-redirect-canary"

	var redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Goog-Api-Key"); got != "" {
			t.Errorf("redirect target received X-Goog-Api-Key %q", got)
		}
	}))
	t.Cleanup(target.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Goog-Api-Key"); got != apiKey {
			t.Errorf("source X-Goog-Api-Key = %q, want canary", got)
		}
		// The target is a different origin and explicitly uses plaintext HTTP.
		// Rejecting all redirects also rejects HTTPS-to-HTTP downgrade redirects.
		http.Redirect(w, r, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	registry := googleTestRegistry(source.URL+"/gemini", apiKey)
	if _, err := registry.FetchModels(t.Context(), "gemini-test"); err == nil {
		t.Fatal("FetchModels error = nil, want rejected redirect status")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect target requests = %d, want 0", got)
	}
}

func googleTestRegistry(baseURL, apiKey string) *backends.ProviderRegistry {
	return backends.NewRegistry(
		backends.WithSettingsService(fakeVault{keys: map[string]string{"gemini-test": apiKey}}),
		backends.WithProviderDefinitions([]backends.ProviderDefinition{{
			Name:           "gemini-test",
			DisplayName:    "Gemini test",
			AdapterType:    backends.AdapterTypes.GOOGLECOMPATIBLE,
			BaseURL:        baseURL,
			DefaultModel:   "gemini-chat",
			APIKeyRequired: true,
			Builtin:        true,
			Enabled:        true,
		}}),
	)
}
