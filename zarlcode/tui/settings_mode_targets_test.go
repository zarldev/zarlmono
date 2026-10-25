package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsShowsPlanAndBuildRouteRows(t *testing.T) {
	ui := newSettingsLayoutUI(t, 160, 60)
	view := ansi.Strip(ui.View().Content)
	for _, label := range []string{"Plan provider", "Plan model", "Build provider", "Build model"} {
		if !strings.Contains(view, label) {
			t.Fatalf("model settings missing %q:\n%s", label, view)
		}
	}
}

func TestSettingsModeRouteActiveSentinelAndCustomModelDoNotChangePrimarySelection(t *testing.T) {
	s := newTestSettings(t)
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(s)
	ui.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for range 7 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.PasteMsg{Content: "custom-plan-model"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	provider, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyMainPlanProvider)
	if err != nil || provider.Value != prefs.ActiveModelTarget {
		t.Fatalf("Plan provider = %q, %v; want active sentinel", provider.Value, err)
	}
	model, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyMainPlanModel)
	if err != nil || model.Value != "custom-plan-model" {
		t.Fatalf("Plan model = %q, %v", model.Value, err)
	}
	for _, key := range []string{prefs.KeyProvider, prefs.KeyModel} {
		if _, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, key); !errors.Is(err, prefs.ErrNotFound) {
			t.Fatalf("route edit changed primary %s: %v", key, err)
		}
	}
}
