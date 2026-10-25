package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsComputerToolsPicker(t *testing.T) {
	s := newTestSettings(t)
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyEnableComputer, "on"); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(s)
	ui.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	for range 6 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"computer tools", "next turn", "host computer APIs", "web_fetch"} {
		if !strings.Contains(view, want) {
			t.Fatalf("computer tools help missing %q:\n%s", want, view)
		}
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyEnableComputer)
	if err != nil || saved.Value != "off" || s.EnableComputer(t.Context()) {
		t.Fatalf("workspace computer setting = %q, %v", saved.Value, err)
	}
	global, err := s.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyEnableComputer)
	if err != nil || global.Value != "on" {
		t.Fatalf("global setting changed = %q, %v", global.Value, err)
	}
}
