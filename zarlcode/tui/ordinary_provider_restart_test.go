package tui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func TestOrdinaryProviderFreshSettingsRestart(t *testing.T) {
	t.Parallel()
	const key = "test-only-credential-canary"
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("wrong persisted endpoint path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("persisted credential was not reconstructed")
		}
		var body struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "custom-model" {
			t.Errorf("persisted model changed: %q", body.Model)
		}
		mu.Lock()
		requests = append(requests, string(body.Messages))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"endpoint answer\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"reply\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	var sessionID string
	for phase := range 2 {
		func() {
			store, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			// Reconstruct every state-owning component from disk, using the same
			// composition/reload/build path as startup without models.dev network IO.
			credentials, err := vault.Open(filepath.Dir(path), func(bool, bool) (string, error) { return "test-restart-passphrase", nil })
			if err != nil {
				t.Fatal(err)
			}
			settings := engine.NewSettings(store, credentials, nil, ws.Root())
			if phase == 0 {
				if err := settings.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
					Name: "custom-restart", DisplayName: "Custom restart", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
					BaseURL: server.URL + "/v1", DefaultModel: "custom-model", ContextWindow: 64000, Enabled: true,
				}); err != nil {
					t.Fatal(err)
				}
				if err := settings.Svc.SetKey(t.Context(), prefs.ScopeWorkspace, "custom-restart", key); err != nil {
					t.Fatal(err)
				}
				requireSetSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "custom-restart", Model: "custom-model"})
			}
			if err := settings.Registry.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			provider, spec, err := settings.BuildActive(t.Context(), engine.ProviderSpec{Name: "llamacpp"})
			if err != nil {
				t.Fatal("rebuild persisted custom provider", err)
			}
			f := &beforeFixture{store: store, ws: ws}
			f.sink = teasink.New(func(msg tea.Msg) { f.mu.Lock(); f.events = append(f.events, msg); f.mu.Unlock() })
			defer f.sink.Close()
			f.live = engine.NewLiveRunner(provider, ws, spec.Model, engine.WithLiveSink(f.sink))
			defer func() {
				if err := f.live.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			}()
			f.live.SetProviderSpec(provider, spec)
			f.ui = tui.New()
			f.ui.SetLiveRunner(f.live)
			f.ui.SetLiveEventSink(f.sink)
			f.ui.SetSettings(settings)
			f.ui.SetProviderContext(engine.ProviderSpec{Name: "llamacpp"}, spec)
			f.ui.SetStartupReady(true)
			prompt := "first persisted prompt"
			if phase == 1 {
				active, err := store.GetSettingExact(t.Context(), ws.Root(), "active_session")
				if err != nil || active != sessionID {
					t.Fatalf("active session lost across restart: %v", err)
				}
				if err := f.ui.ResumeSavedSession(t.Context(), active); err != nil {
					t.Fatal(err)
				}
				prompt = "second prompt after restart"
			}
			settleRewindTurn(t, f, prompt)
			sessionID = f.ui.SessionIdentity()
			record, err := store.GetSession(t.Context(), sessionID)
			if err != nil || record.Provider != spec.Name || record.Model != spec.Model {
				t.Fatalf("saved target mismatch: %v", err)
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), key) || strings.Contains(f.ui.ToastText(), key) {
				t.Fatal("credential leaked into session data or diagnostics")
			}
		}()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("custom endpoint received %d calls, want 2", len(requests))
	}
	for _, text := range []string{"first persisted prompt", "endpoint answer", "second prompt after restart"} {
		if !strings.Contains(requests[1], text) {
			t.Errorf("resumed provider request omitted %q", text)
		}
	}
}
