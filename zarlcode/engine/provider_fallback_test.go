package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestActiveProviderKeepsFallbackCredentialsOnlyForFallbackBackend(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	fallback := engine.ProviderSpec{
		Name:        "llamacpp",
		Model:       "fallback-model",
		BaseURL:     "http://127.0.0.1:8081",
		APIKey:      "fallback-key",
		CodexEffort: "high",
	}

	got := settings.ActiveProvider(t.Context(), fallback)
	if got != fallback {
		t.Fatalf("ActiveProvider() = %#v, want fallback %#v", got, fallback)
	}

	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider, "openai"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyModel, "configured-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	got = settings.ActiveProvider(t.Context(), fallback)
	if got.Name != "openai" || got.Model != "configured-model" {
		t.Fatalf("ActiveProvider() selection = %#v", got)
	}
	if got.BaseURL != "" || got.APIKey != "" {
		t.Fatalf("fallback credentials leaked to selected backend: %#v", got)
	}
}

func TestInheritedClaudeCodeDoesNotReplaceFallback(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyProvider, "claude-code"); err != nil {
		t.Fatalf("set global provider: %v", err)
	}
	fallback := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	if got := settings.ActiveProvider(t.Context(), fallback); got.Name != fallback.Name {
		t.Fatalf("inherited claude-code selected %q, want fallback %q", got.Name, fallback.Name)
	}

	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider, "claude-code"); err != nil {
		t.Fatalf("set workspace provider: %v", err)
	}
	if got := settings.ActiveProvider(t.Context(), fallback); got.Name != "claude-code" {
		t.Fatalf("workspace claude-code selected %q", got.Name)
	}
}
