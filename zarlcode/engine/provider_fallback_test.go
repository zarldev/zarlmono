package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestActiveProviderKeepsFallbackCredentialsOnlyForFallbackBackend(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	fallback := engine.ProviderSpec{
		Name:    "llamacpp",
		Model:   "fallback-model",
		BaseURL: "http://127.0.0.1:8081",
		APIKey:  "fallback-key",
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

func TestActiveProviderPairsInheritedModelWithProvider(t *testing.T) {
	for _, tc := range []struct {
		name           string
		globalProvider string
		workspace      string
		globalModel    string
		wantModel      string
	}{
		{name: "different global provider", globalProvider: "openai", workspace: "deepseek", globalModel: "global-openai", wantModel: "deepseek-flash"},
		{name: "same global provider", globalProvider: "deepseek", workspace: "deepseek", globalModel: "global-deepseek", wantModel: "global-deepseek"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			settings := engine.NewSettings(store, nil, nil, t.TempDir())
			if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeGlobal, prefs.ModelSelection{Provider: tc.globalProvider, Model: tc.globalModel}); err != nil {
				t.Fatal(err)
			}
			if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: tc.workspace}); err != nil {
				t.Fatal(err)
			}
			got := settings.ActiveProvider(t.Context(), engine.ProviderSpec{Name: "llamacpp", Model: "fallback-model"})
			if got.Name != tc.workspace || got.Model != tc.wantModel {
				t.Fatalf("ActiveProvider() = %s/%s, want %s/%s", got.Name, got.Model, tc.workspace, tc.wantModel)
			}
		})
	}
}

func TestActiveProviderKeepsLegacyGlobalModelOnFallbackRoute(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyModel, "legacy-model"); err != nil {
		t.Fatal(err)
	}
	got := settings.ActiveProvider(t.Context(), engine.ProviderSpec{Name: "llamacpp", Model: "fallback-model"})
	if got.Name != "llamacpp" || got.Model != "legacy-model" {
		t.Fatalf("ActiveProvider() = %s/%s, want fallback provider with legacy model", got.Name, got.Model)
	}
}

func TestInheritedClaudeCodeDoesNotReplaceFallback(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeGlobal, prefs.ModelSelection{Provider: "claude-code", Model: "inherited-claude-model"}); err != nil {
		t.Fatalf("set global selection: %v", err)
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyModel, "workspace-model-for-ignored-route"); err != nil {
		t.Fatalf("set workspace model: %v", err)
	}
	fallback := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	if got := settings.ActiveProvider(t.Context(), fallback); got.Name != fallback.Name || got.Model != fallback.Model {
		t.Fatalf("inherited claude-code resolved to %q/%q, want fallback %q/%q", got.Name, got.Model, fallback.Name, fallback.Model)
	}

	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "claude-code", Model: "workspace-claude-model"}); err != nil {
		t.Fatalf("set workspace selection: %v", err)
	}
	if got := settings.ActiveProvider(t.Context(), fallback); got.Name != "claude-code" || got.Model != "workspace-claude-model" {
		t.Fatalf("workspace claude-code resolved to %q/%q", got.Name, got.Model)
	}
}
