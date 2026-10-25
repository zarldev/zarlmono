package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsDisplaysApplicationDefaults(t *testing.T) {
	ui := newSettingsLayoutUI(t, 180, 70)
	for category, rows := range []map[string]string{
		{"provider": "openai-codex", "model": "gpt-6-astra", "reasoning effort": "high", "text verbosity": "(unset)"},
		nil, nil,
		{"reserve tokens": "5120", "tool result max kb": "512", "tool result max lines": "3500", "model": "gpt-6-sol"},
		{"max iterations": "900", "spawn max iterations": "300", "max concurrent": "20", "program parallel calls": "16", "explore provider": "deepseek", "verify model": "gpt-6-luna"},
		{"shell policy": "off", "sandbox": "off", "sudo askpass": "on", "read before write": "off", "judge model": "deepseek-v4-flash"},
		{"mcp schema exposure": "deferred", "computer tools": "on"},
	} {
		if category > 0 {
			ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		lines := strings.Split(ansi.Strip(ui.View().Content), "\n")
		for label, want := range rows {
			found := false
			for _, line := range lines {
				if strings.Contains(line, label) && strings.Contains(line, want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("category %d missing %s=%s:\n%s", category, label, want, strings.Join(lines, "\n"))
			}
		}
	}
}

func TestSettingsCustomCompactionModelKeepsDisplayedProvider(t *testing.T) {
	s := newTestSettings(t)
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(s)
	ui.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	for range 3 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // context category
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for range 6 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // compaction model
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // no models fetched: custom editor
	ui.Update(tea.PasteMsg{Content: "custom-compact-model"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	provider, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactProvider)
	if err != nil || provider.Value != "openai-codex" {
		t.Fatalf("displayed provider not retained: %q, %v", provider.Value, err)
	}
	model, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactModel)
	if err != nil || model.Value != "custom-compact-model" {
		t.Fatalf("model not saved: %q, %v", model.Value, err)
	}
}

func TestSettingsActiveCompactionProviderOptsOutOfDefaultRoute(t *testing.T) {
	s := newTestSettings(t)
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(s)
	ui.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	for range 3 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for range 5 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for range len(s.Registry.All()) {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // first entry is (active)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	provider, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactProvider)
	if err != nil || provider.Value != prefs.ActiveModelTarget {
		t.Fatalf("active provider choice not retained: %q, %v", provider.Value, err)
	}
	if got := s.ModelTarget(t.Context(), prefs.KeyCompactProvider); got.Name != "" || got.Model != "" {
		t.Fatalf("active choice re-enabled default route: %+v", got)
	}
}

func TestSettingsModelHelpDistinguishesBuiltInFromSavedRoute(t *testing.T) {
	s := newTestSettings(t)
	for key, value := range map[string]string{prefs.KeyCompactProvider: "anthropic", prefs.KeyCompactModel: "custom-model"} {
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, key, value); err != nil {
			t.Fatal(err)
		}
	}
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(s)
	ui.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	for range 3 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for range 6 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"custom-model", "built-in default gpt-6-sol", "effective source global default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings help missing %q:\n%s", want, view)
		}
	}
}
