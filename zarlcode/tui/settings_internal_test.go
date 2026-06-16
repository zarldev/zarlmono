package tui

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// newTestSettings assembles a engine.Settings over a throwaway sqlite db (no
// vault — plaintext settings only). Mirrors what engine.OpenSettings builds.
func newTestSettings(t *testing.T) *engine.Settings {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return engine.NewSettings(t.Context(), store, nil, dir)
}

func TestActiveProvider_Precedence(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	fb := engine.ProviderSpec{Name: "llamacpp", Model: "local"}

	// No rows set → falls back to the caller's env-derived defaults.
	if got := s.ActiveProvider(ctx, fb); got.Name != "llamacpp" || got.Model != "local" {
		t.Fatalf("unset: got %+v, want llamacpp/local", got)
	}

	// Global rows override the fallback.
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProvider, "openai"); err != nil {
		t.Fatal(err)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyModel, "gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fb); got.Name != "openai" || got.Model != "gpt-4o-mini" {
		t.Fatalf("global: got %+v, want openai/gpt-4o-mini", got)
	}

	// A workspace pin shadows the global default (effective precedence).
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel, "pinned"); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fb); got.Model != "pinned" {
		t.Fatalf("workspace pin: got model %q, want pinned", got.Model)
	}
}

// claude-code spawns the `claude` CLI, so it must never be the inherited
// default — only an explicit per-workspace pin counts.
func TestActiveProvider_ClaudeCodeNeverInheritedDefault(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	fb := engine.ProviderSpec{Name: "llamacpp", Model: "local"}

	// A global setting (e.g. carried over from v1) must NOT auto-select it.
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProvider, backends.NameClaudeCode.String()); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fb); got.Name != "llamacpp" {
		t.Errorf("inherited claude-code should be ignored, got %q", got.Name)
	}

	// An explicit workspace pin IS honoured (option a: still usable).
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyProvider, backends.NameClaudeCode.String()); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fb); got.Name != backends.NameClaudeCode.String() {
		t.Errorf("workspace-pinned claude-code should be honoured, got %q", got.Name)
	}
}

func TestConfirmQuitDefaultsOnAndCanBeDisabled(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)

	if !s.ConfirmQuit(ctx) {
		t.Fatal("unset confirm_quit should default to on")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyConfirmQuit, "off"); err != nil {
		t.Fatal(err)
	}
	if s.ConfirmQuit(ctx) {
		t.Fatal("confirm_quit=off should disable the quit confirmation")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyConfirmQuit, "on"); err != nil {
		t.Fatal(err)
	}
	if !s.ConfirmQuit(ctx) {
		t.Fatal("confirm_quit=on should enable the quit confirmation")
	}
}

func TestShellSandboxDefaultsOnAndCanBeDisabled(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)

	if !s.ShellSandbox(ctx) {
		t.Fatal("unset sandbox should default to on")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeySandbox, "off"); err != nil {
		t.Fatal(err)
	}
	if s.ShellSandbox(ctx) {
		t.Fatal("sandbox=off should disable shell confinement")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeySandbox, "on"); err != nil {
		t.Fatal(err)
	}
	if !s.ShellSandbox(ctx) {
		t.Fatal("sandbox=on should enable shell confinement")
	}
}

// engine.BuildProvider must route per authentication method: OAuth backends need
// the vault, registry-backed backends need the registry, and a local
// OpenAI-compatible backend builds with no key/network.
func TestBuildProvider_DispatchPerMethod(t *testing.T) {
	ctx := t.Context()

	// OAuth backends without a vault are refused (no creds loadable).
	for _, name := range []string{backends.NameOpenAICodex.String(), backends.NameClaudeCode.String()} {
		if _, err := engine.BuildProvider(ctx, nil, nil, engine.ProviderSpec{Name: name, Model: "x"}); err == nil {
			t.Errorf("%s without vault should error", name)
		}
	}

	// Registry-backed backend with no registry is refused.
	if _, err := engine.BuildProvider(ctx, nil, nil, engine.ProviderSpec{Name: "openai", Model: "x"}); err == nil {
		t.Error("registry-backed provider with nil registry should error")
	}

	// Local OpenAI-compatible (llamacpp) builds through the registry with a
	// placeholder key and no network call.
	s := newTestSettings(t)
	if _, err := engine.BuildProvider(ctx, s.Registry, s.Svc, engine.ProviderSpec{Name: "llamacpp", Model: "local"}); err != nil {
		t.Errorf("llamacpp build: %v", err)
	}
}
