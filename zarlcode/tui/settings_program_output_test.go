package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsProgramOutputRecoveryPicker(t *testing.T) {
	s := newTestSettings(t)
	if s.ProgramOutputRecovery(t.Context()) {
		t.Fatal("recovery must default off")
	}
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyProgramOutputRecovery, "off"); err != nil {
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
	for range 3 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"program output recovery", "1 MiB/result", "15 minutes", "transcript policy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing help %q:\n%s", want, view)
		}
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProgramOutputRecovery)
	if err != nil || saved.Value != "on" || !s.ProgramOutputRecovery(t.Context()) {
		t.Fatalf("workspace preference = %+v, %v", saved, err)
	}
	global, err := s.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyProgramOutputRecovery)
	if err != nil || global.Value != "off" {
		t.Fatalf("global preference changed = %+v, %v", global, err)
	}
}
