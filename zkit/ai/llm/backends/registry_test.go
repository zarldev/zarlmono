package backends_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

type fakeKeyService struct{}

func (fakeKeyService) GetKey(context.Context, string) (string, bool, error) { return "", false, nil }
func (fakeKeyService) HasVault() bool                                       { return false }

// fakeVault is a backends.SettingsService with a populated, name-keyed vault.
type fakeVault struct{ keys map[string]string }

func (f fakeVault) GetKey(_ context.Context, provider string) (string, bool, error) {
	k, ok := f.keys[provider]
	return k, ok, nil
}
func (fakeVault) HasVault() bool { return true }

// fakeStore is an in-memory backends.Store for tests — no sqlite, no
// dependency on zarlcode/db. Custom providers and settings live in
// maps; concurrency-safe so parallel subtests can share one.
type fakeStore struct {
	mu        sync.Mutex
	providers map[string]backends.StoredProvider
}

func newFakeStore() *fakeStore {
	return &fakeStore{providers: map[string]backends.StoredProvider{}}
}

func (s *fakeStore) ListProviders(context.Context) ([]backends.StoredProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]backends.StoredProvider, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p)
	}
	return out, nil
}

func (s *fakeStore) UpsertProvider(_ context.Context, p backends.StoredProvider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[p.Name] = p
	return nil
}

func (s *fakeStore) DeleteProvider(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.providers, name)
	return nil
}

func TestRegistryBuildLocalProvidersAreNamed(t *testing.T) {
	ctx := t.Context()
	reg := backends.NewRegistry(backends.WithStore(newFakeStore()), backends.WithSettingsService(fakeKeyService{}))
	for _, name := range []string{"llamacpp", "ollama"} {
		p, err := reg.Build(ctx, name, "test-model")
		if err != nil {
			t.Fatalf("Build(%q): %v", name, err)
		}
		if got := p.Name(); got != name {
			t.Fatalf("Build(%q).Name() = %q, want %q", name, got, name)
		}
	}
}

// TestRegistryBuildBuiltinsDoNotPanic builds every static-key built-in
// through the adapter layer to catch adapterDef type-parameter mismatches
// (the deepseek facade type once panicked here on a bad type assertion).
func TestRegistryBuildBuiltinsDoNotPanic(t *testing.T) {
	reg, ctx := newTestRegistry(t, fakeVault{keys: map[string]string{
		"openai":    "sk-x",
		"deepseek":  "sk-x",
		"anthropic": "sk-x",
		"gemini":    "sk-x",
	}})
	cases := []struct{ name, want string }{
		{"openai", "openai"},
		{"deepseek", "deepseek"},
		{"llamacpp", "llamacpp"},
		{"ollama", "ollama"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := reg.Build(ctx, tc.name, "")
			if err != nil {
				t.Fatalf("Build(%q): %v", tc.name, err)
			}
			if got := p.Name(); got != tc.want {
				t.Fatalf("Build(%q).Name() = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestRegistryBuildNilStoreAndService exercises the documented
// built-ins-only configuration: nil Store and nil SettingsService, with
// keys resolved from the environment alone. resolveAPIKey once panicked
// here on the unguarded vault check.
func TestRegistryBuildNilStoreAndService(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-x")
	ctx := t.Context()
	reg := backends.NewRegistry()
	for _, name := range []string{"openai", "llamacpp"} {
		if _, err := reg.Build(ctx, name, ""); err != nil {
			t.Fatalf("Build(%q): %v", name, err)
		}
	}
}

func newTestRegistry(t *testing.T, svc backends.SettingsService) (*backends.ProviderRegistry, context.Context) {
	t.Helper()
	return backends.NewRegistry(backends.WithStore(newFakeStore()), backends.WithSettingsService(svc)), t.Context()
}

// upsertCustomOAI registers a custom OpenAI-compatible provider pointing at
// baseURL with the given seed list.
func upsertCustomOAI(t *testing.T, reg *backends.ProviderRegistry, ctx context.Context, name, baseURL string, seeds []string) {
	t.Helper()
	if err := reg.UpsertProvider(ctx, backends.ProviderDefinition{
		Name:        name,
		DisplayName: name,
		AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:     baseURL,
		SeedModels:  seeds,
		Enabled:     true,
	}); err != nil {
		t.Fatalf("UpsertProvider(%q): %v", name, err)
	}
}

func TestFetchModelsFollowsOpenAIPagination(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		pages = append(pages, r.URL.Query().Get("after"))
		switch r.URL.Query().Get("after") {
		case "":
			io.WriteString(w, `{"data":[{"id":"alpha"}],"has_more":true,"last_id":"alpha"}`)
		case "alpha":
			io.WriteString(w, `{"data":[{"id":"beta"}],"has_more":false,"last_id":"beta"}`)
		default:
			t.Fatalf("unexpected after cursor %q", r.URL.Query().Get("after"))
		}
	}))
	defer srv.Close()

	reg, ctx := newTestRegistry(t, fakeKeyService{})
	upsertCustomOAI(t, reg, ctx, "custom-oai", srv.URL, nil)

	models, err := reg.FetchModels(ctx, "custom-oai")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if want := []string{"alpha", "beta"}; !slices.Equal(models, want) {
		t.Fatalf("FetchModels = %v, want %v", models, want)
	}
	if want := []string{"", "alpha"}; !slices.Equal(pages, want) {
		t.Fatalf("page cursors = %v, want %v", pages, want)
	}
}

func TestFetchModelsFollowsAnthropicPagination(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != "sk-ant-test" {
			t.Fatalf("x-api-key = %q, want sk-ant-test", r.Header.Get("X-Api-Key"))
		}
		if r.URL.Query().Get("limit") != "1000" {
			t.Fatalf("limit = %q, want 1000", r.URL.Query().Get("limit"))
		}
		pages = append(pages, r.URL.Query().Get("after_id"))
		switch r.URL.Query().Get("after_id") {
		case "":
			io.WriteString(w, `{"data":[{"id":"claude-a"}],"has_more":true,"last_id":"claude-a"}`)
		case "claude-a":
			io.WriteString(w, `{"data":[{"id":"claude-b"}],"has_more":false,"last_id":"claude-b"}`)
		default:
			t.Fatalf("unexpected after_id cursor %q", r.URL.Query().Get("after_id"))
		}
	}))
	defer srv.Close()

	reg, ctx := newTestRegistry(t, fakeVault{keys: map[string]string{"custom-anthropic": "sk-ant-test"}})
	if err := reg.UpsertProvider(ctx, backends.ProviderDefinition{
		Name:        "custom-anthropic",
		DisplayName: "custom-anthropic",
		AdapterType: backends.AdapterTypes.ANTHROPICCOMPATIBLE,
		BaseURL:     srv.URL,
		Enabled:     true,
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	models, err := reg.FetchModels(ctx, "custom-anthropic")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if want := []string{"claude-a", "claude-b"}; !slices.Equal(models, want) {
		t.Fatalf("FetchModels = %v, want %v", models, want)
	}
	if want := []string{"", "claude-a"}; !slices.Equal(pages, want) {
		t.Fatalf("page cursors = %v, want %v", pages, want)
	}
}

func TestFetchModelsLiveProbe(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"data":[{"id":"alpha"},{"id":"beta"}]}`)
	}))
	defer srv.Close()

	// No vault, no env key → the probe must go out unauthenticated.
	reg, ctx := newTestRegistry(t, fakeKeyService{})
	upsertCustomOAI(t, reg, ctx, "custom-oai", srv.URL, []string{"seed-only"})

	models, err := reg.FetchModels(ctx, "custom-oai")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if want := []string{"alpha", "beta"}; !slices.Equal(models, want) {
		t.Fatalf("FetchModels = %v, want %v", models, want)
	}
	if gotAuth != "" {
		t.Fatalf("keyless probe sent Authorization = %q, want none", gotAuth)
	}
}

// TestFetchModelsSendsBearerForCustomWithStoredKey is the regression test
// for the custom-provider key-resolution bug: a key saved in the vault
// under a custom provider's name must be resolved and sent as a bearer on
// the /models probe, even though customs report RequiresKey() == false.
func TestFetchModelsSendsBearerForCustomWithStoredKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"data":[{"id":"gamma"}]}`)
	}))
	defer srv.Close()

	reg, ctx := newTestRegistry(t, fakeVault{keys: map[string]string{"custom-oai": "sk-test"}})
	upsertCustomOAI(t, reg, ctx, "custom-oai", srv.URL, []string{"seed-only"})

	models, err := reg.FetchModels(ctx, "custom-oai")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if want := []string{"gamma"}; !slices.Equal(models, want) {
		t.Fatalf("FetchModels = %v, want %v", models, want)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-test")
	}
}

func TestFetchModelsFallsBackToSeedsOnError(t *testing.T) {
	reg, ctx := newTestRegistry(t, fakeKeyService{})
	upsertCustomOAI(t, reg, ctx, "dead-oai", "http://127.0.0.1:1", []string{"s1", "s2"}) // connection refused

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	models, err := reg.FetchModels(cctx, "dead-oai")
	if err != nil {
		t.Fatalf("FetchModels should fall back, got err: %v", err)
	}
	if want := []string{"s1", "s2"}; !slices.Equal(models, want) {
		t.Fatalf("FetchModels = %v, want seed fallback %v", models, want)
	}
}

// TestFetchModelsOAuthBuiltinsReturnPresets is the regression test for the
// "openai-codex shows no models to select from" bug: the OAuth builtins
// have no live /models probe and resolve no API key, so FetchModels has to
// fall back to their seed list. Empty seeds (the old state) left the picker
// blank. Both codex and claude-code must surface their preset catalogue.
func TestFetchModelsOAuthBuiltinsReturnPresets(t *testing.T) {
	reg, ctx := newTestRegistry(t, fakeKeyService{}) // resolves no key — the OAuth case
	for _, name := range []string{"openai-codex", "claude-code"} {
		t.Run(name, func(t *testing.T) {
			models, err := reg.FetchModels(ctx, name)
			if err != nil {
				t.Fatalf("FetchModels(%q): %v", name, err)
			}
			if len(models) == 0 {
				t.Fatalf("FetchModels(%q) = empty, want preset models", name)
			}
			if name == "claude-code" && !slices.Contains(models, "fable") {
				t.Fatalf("FetchModels(%q) = %v, want fable", name, models)
			}
		})
	}
}

// TestUsesAPIKey locks in the distinction between RequiresKey (env-var
// declaration) and UsesAPIKey (offer a key field in the UI).
func TestUsesAPIKey(t *testing.T) {
	tests := []struct {
		name string
		def  backends.ProviderDefinition
		want bool
	}{
		{"hosted builtin", backends.ProviderDefinition{Builtin: true, EnvAPIKeyVars: []string{"OPENAI_API_KEY"}}, true},
		{"local builtin", backends.ProviderDefinition{Builtin: true}, false},
		{"custom no env", backends.ProviderDefinition{Builtin: false}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.def.UsesAPIKey(); got != tc.want {
				t.Fatalf("UsesAPIKey() = %v, want %v", got, tc.want)
			}
		})
	}
}
