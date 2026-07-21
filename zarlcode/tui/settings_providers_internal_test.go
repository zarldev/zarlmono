package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// gotoCat moves the nav cursor to the named category.
func gotoCat(d *settingsDialog, name string) bool {
	for i, c := range d.cats {
		if c.name == name {
			d.cat = i
			d.focusRows = false
			return true
		}
	}
	return false
}

func TestSettingsDialog_ProvidersInline(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "providers") {
		t.Fatal("Providers category missing")
	}
	if !d.cats[d.cat].providers {
		t.Fatal("Providers category should be flagged as the inline panel")
	}

	// Enter the panel and drive it inline (no popup is pushed).
	d.handleKey(skey(tea.KeyTab)) // focus the detail panel
	if !d.focusRows {
		t.Fatal("tab should focus the providers panel")
	}
	gotoProvider(d.providers, "openai")
	d.handleKey(tkey("a")) // set active — delegated to the panel
	if sv, ok, _ := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider); !ok || sv.Value != "openai" {
		t.Errorf("inline 'set active' did not persist: ok=%v val=%q", ok, sv.Value)
	}

	// 'n' opens the inline add form (still no popup).
	d.handleKey(tkey("n"))
	if !d.providers.adding {
		t.Error("`n` should open the inline add form in the panel")
	}
	d.handleKey(skey(tea.KeyEscape)) // cancel add
	if d.providers.adding {
		t.Error("esc should cancel the add form")
	}

	// esc with the panel idle returns focus to the nav (doesn't close).
	d.handleKey(skey(tea.KeyEscape))
	if d.focusRows {
		t.Error("esc should return focus to the category nav")
	}
}

func TestSettingsDialog_ProviderModelsOpenPickerAndPersistSelection(t *testing.T) {
	s := newTestSettingsWithVault(t)
	if err := s.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
		Name:         "custom",
		DisplayName:  "Custom",
		AdapterType:  backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:      "https://example.com/v1",
		DefaultModel: "custom-a",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	d := newSettingsDialog(s)
	if !gotoCat(d, "providers") {
		t.Fatal("Providers category missing")
	}
	d.handleKey(skey(tea.KeyTab))
	gotoProvider(d.providers, "custom")

	a := d.handleKey(tkey("m"))
	fetch, ok := a.(actionFetchModels)
	if !ok || fetch.provider != "custom" {
		t.Fatalf("models action = %#v, want custom fetch", a)
	}
	d.onModelsLoaded("custom", []string{"custom-a", "custom-b"}, nil)
	if d.providers.modelPicker == nil {
		t.Fatal("provider model fetch should open a picker")
	}

	d.handleKey(skey(tea.KeyDown))
	d.handleKey(skey(tea.KeyEnter))
	if d.providers.modelPicker != nil {
		t.Fatal("selecting a model should close the picker")
	}
	if sv, ok, _ := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider); !ok || sv.Value != "custom" {
		t.Fatalf("provider setting = ok %v value %q, want custom", ok, sv.Value)
	}
	if sv, ok, _ := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyModel); !ok || sv.Value != "custom-b" {
		t.Fatalf("model setting = ok %v value %q, want custom-b", ok, sv.Value)
	}
}

func TestStartOAuthLogin_ClaudeCodeUsesExec(t *testing.T) {
	prev := openBrowser
	openBrowser = func(string) {}
	defer func() { openBrowser = prev }()

	s := newTestSettingsWithVault(t)
	m := New()
	m.SetSettings(s)
	d := newSettingsDialog(s)
	m.overlay.push(d)

	cmd := m.startOAuthLogin(backends.NameClaudeCode.String())
	if cmd == nil {
		t.Fatal("claude-code sign-in with a vault should return an exec command")
	}
	// Don't run cmd — it would spawn `claude setup-token`.
}
