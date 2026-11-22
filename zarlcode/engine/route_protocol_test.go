package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

type routeProtocolRequest struct {
	path, alias string
}

func routeProtocolSettings(t *testing.T) (*engine.Settings, string, <-chan routeProtocolRequest) {
	t.Helper()
	requests := make(chan routeProtocolRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if strings.HasPrefix(r.URL.Path, "/gateway/v1beta/models/") {
			body.Model = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/gateway/v1beta/models/"), ":generateContent")
		}
		requests <- routeProtocolRequest{path: r.URL.Path, alias: body.Model}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gateway/v1/chat/completions":
			_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
		case "/gateway/v1/responses":
			_, _ = io.WriteString(w, `{"id":"r1","status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
		case "/gateway/v1/messages":
			_, _ = io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","model":"ignored","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		default:
			if strings.HasPrefix(r.URL.Path, "/gateway/v1beta/models/") {
				_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
			} else {
				t.Errorf("unexpected protocol path: %s", r.URL.Path)
				http.Error(w, "unexpected protocol", http.StatusBadRequest)
			}
		}
	}))
	t.Cleanup(server.Close)
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return engine.NewSettings(store, nil, nil, t.TempDir()), server.URL + "/gateway", requests
}

func assertRouteProtocolHTTP(t *testing.T, provider llm.Provider, requests <-chan routeProtocolRequest, path, alias string) {
	t.Helper()
	var text strings.Builder
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(chunk.Content)
	}
	if text.String() != "done" {
		t.Fatalf("answer = %q", text.String())
	}
	select {
	case got := <-requests:
		if got.path != path || got.alias != alias {
			t.Fatalf("request = %#v, want path %q alias %q", got, path, alias)
		}
	default:
		t.Fatal("no HTTP request")
	}
}

func pinRouteProtocolAlias(t *testing.T, settings *engine.Settings, provider, alias string) {
	t.Helper()
	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: provider, Model: alias}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildActiveDurableRouteProtocolsDoNotBecomeRuntimeIntent(t *testing.T) {
	t.Parallel()
	settings, baseURL, requests := routeProtocolSettings(t)
	const first = " Company/Production Ω alias "
	const second = "Responses/alias"
	for _, row := range []struct {
		alias    string
		protocol llm.ModelProtocol
	}{{first, llm.ModelProtocols.PROTOCOLMESSAGES}, {second, llm.ModelProtocols.PROTOCOLRESPONSES}, {"Google/alias", llm.ModelProtocols.PROTOCOLGENERATECONTENT}} {
		if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", row.alias, row.protocol); err != nil {
			t.Fatal(err)
		}
	}
	fallback := engine.ProviderSpec{Name: "litellm", Model: first, BaseURL: baseURL, APIKey: "fixture"}
	providerA, specA, err := settings.BuildActive(t.Context(), fallback)
	if err != nil {
		t.Fatal(err)
	}
	if specA.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		t.Fatal("configured policy became runtime intent")
	}
	assertRouteProtocolHTTP(t, providerA, requests, "/gateway/v1/messages", first)

	// Reuse the returned selection as fallback while switching aliases on the
	// same provider. A's policy must not be carried into B as an override.
	pinRouteProtocolAlias(t, settings, "litellm", second)
	providerB, specB, err := settings.BuildActive(t.Context(), specA)
	if err != nil {
		t.Fatal(err)
	}
	if specB.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || specB.Model != second {
		t.Fatalf("alias B selection = %#v", specB)
	}
	assertRouteProtocolHTTP(t, providerB, requests, "/gateway/v1/responses", second)

	for _, alias := range []string{first, second} {
		pinRouteProtocolAlias(t, settings, "litellm", alias)
		explicit := specB
		explicit.Protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
		provider, spec, err := settings.BuildActive(t.Context(), explicit)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Protocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
			t.Fatal("explicit override intent lost")
		}
		assertRouteProtocolHTTP(t, provider, requests, "/gateway/v1/chat/completions", alias)
	}
	if got, err := settings.Registry.GetRouteProtocol(t.Context(), "litellm", first); err != nil || got != llm.ModelProtocols.PROTOCOLMESSAGES {
		t.Fatalf("runtime override changed A policy: %s, %v", got, err)
	}
	if got, err := settings.Registry.GetRouteProtocol(t.Context(), "litellm", second); err != nil || got != llm.ModelProtocols.PROTOCOLRESPONSES {
		t.Fatalf("runtime override changed B policy: %s, %v", got, err)
	}
	pinRouteProtocolAlias(t, settings, "litellm", first)
	if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", first, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	// Existing built providers retain their original codec; only future builds
	// observe policy changes, without mutating the caller's runtime intent.
	assertRouteProtocolHTTP(t, providerA, requests, "/gateway/v1/messages", first)
	updated, _, err := settings.BuildActive(t.Context(), specB)
	if err != nil {
		t.Fatal(err)
	}
	assertRouteProtocolHTTP(t, updated, requests, "/gateway/v1/responses", first)
	if err := settings.Registry.DeleteRouteProtocol(t.Context(), "litellm", first); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ alias, path string }{
		{first, "/gateway/v1/chat/completions"},
		{"absent", "/gateway/v1/chat/completions"},
		{"Google/alias", "/gateway/v1beta/models/Google/alias:generateContent"},
	} {
		pinRouteProtocolAlias(t, settings, "litellm", tc.alias)
		provider, spec, err := settings.BuildActive(t.Context(), specA)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
			t.Fatal("resolved policy became runtime intent")
		}
		assertRouteProtocolHTTP(t, provider, requests, tc.path, tc.alias)
	}
}

func TestBuildActiveRouteProtocolsIsolateNamedGateways(t *testing.T) {
	t.Parallel()
	settings, baseURL, requests := routeProtocolSettings(t)
	const alias = "Same/opaque Ω alias"
	for _, tc := range []struct {
		name, path string
		protocol   llm.ModelProtocol
	}{
		{"gateway-one", "/gateway/v1/responses", llm.ModelProtocols.PROTOCOLRESPONSES},
		{"gateway-two", "/gateway/v1/messages", llm.ModelProtocols.PROTOCOLMESSAGES},
	} {
		def, _ := backends.Builtin("litellm")
		def.Name, def.Builtin, def.BaseURL, def.DefaultModel = tc.name, false, baseURL, alias
		if err := settings.Registry.UpsertProvider(t.Context(), def); err != nil {
			t.Fatal(err)
		}
		if err := settings.Registry.SetRouteProtocol(t.Context(), tc.name, alias, tc.protocol); err != nil {
			t.Fatal(err)
		}
		pinRouteProtocolAlias(t, settings, tc.name, alias)
		provider, spec, err := settings.BuildActive(t.Context(), engine.ProviderSpec{Name: tc.name, APIKey: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if provider.Name() != tc.name || spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
			t.Fatalf("named gateway selection = %s, %#v", provider.Name(), spec)
		}
		assertRouteProtocolHTTP(t, provider, requests, tc.path, alias)
	}
}
