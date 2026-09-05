package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestActiveProviderPrecedence(t *testing.T) {
	ctx, s := t.Context(), newTestSettings(t)
	fallback := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	if got := s.ActiveProvider(ctx, fallback); got != fallback {
		t.Fatalf("unset provider = %+v", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProvider, "openai"); err != nil {
		t.Fatal(err)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyModel, "gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fallback); got.Name != "openai" || got.Model != "gpt-4o-mini" {
		t.Fatalf("global provider = %+v", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel, "pinned"); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fallback); got.Model != "pinned" {
		t.Fatalf("workspace model = %q", got.Model)
	}
}

func TestActiveProviderClaudeCodeNeverInheritedDefault(t *testing.T) {
	ctx, s := t.Context(), newTestSettings(t)
	fallback := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	name := backends.NameClaudeCode.String()
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProvider, name); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fallback); got.Name != fallback.Name {
		t.Fatalf("inherited claude-code selected: %q", got.Name)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyProvider, name); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProvider(ctx, fallback); got.Name != name {
		t.Fatalf("workspace pin ignored: %q", got.Name)
	}
}

func TestBooleanSettingsDefaultOnAndCanBeDisabled(t *testing.T) {
	ctx, s := t.Context(), newTestSettings(t)
	for _, tc := range []struct {
		name, key string
		get       func() bool
	}{
		{"confirm quit", prefs.KeyConfirmQuit, func() bool { return s.ConfirmQuit(ctx) }},
		{"shell sandbox", prefs.KeySandbox, func() bool { return s.ShellSandbox(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.get() {
				t.Fatal("unset should default on")
			}
			if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, tc.key, "off"); err != nil {
				t.Fatal(err)
			}
			if tc.get() {
				t.Fatal("off should disable")
			}
			if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, tc.key, "on"); err != nil {
				t.Fatal(err)
			}
			if !tc.get() {
				t.Fatal("on should enable")
			}
		})
	}
}

func TestBuildProviderDispatchPerMethod(t *testing.T) {
	ctx := t.Context()
	for _, name := range []string{backends.NameOpenAICodex.String(), backends.NameClaudeCode.String()} {
		if _, err := engine.BuildProvider(ctx, nil, nil, engine.ProviderSpec{Name: name, Model: "x"}); err == nil {
			t.Errorf("%s without vault should error", name)
		}
	}
	if _, err := engine.BuildProvider(ctx, nil, nil, engine.ProviderSpec{Name: "openai", Model: "x"}); err == nil {
		t.Error("registry provider without registry should error")
	}
	s := newTestSettings(t)
	if _, err := engine.BuildProvider(ctx, s.Registry, s.Svc, engine.ProviderSpec{Name: "llamacpp", Model: "local"}); err != nil {
		t.Errorf("llamacpp build: %v", err)
	}
}
