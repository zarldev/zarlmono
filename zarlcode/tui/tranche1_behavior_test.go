package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestDashboardRoutesTabKeysBeforeShellShortcuts(t *testing.T) {
	harness := tui.NewDashboardRoutingHarness()

	harness.Press(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := harness.Tab(); got != "context" {
		t.Fatalf("Tab selected %q, want context", got)
	}

	harness.Press(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := harness.Tab(); got != "overview" {
		t.Fatalf("Shift+Tab selected %q, want overview", got)
	}
	if harness.PlanMode() {
		t.Fatal("Shift+Tab toggled Plan mode while dashboard was expanded")
	}

	harness.Press(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := harness.Tab(); got != "context" {
		t.Fatalf("Right selected %q, want context", got)
	}
	harness.Press(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := harness.Tab(); got != "overview" {
		t.Fatalf("Left selected %q, want overview", got)
	}

	harness.Collapse()
	harness.Press(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !harness.PlanMode() {
		t.Fatal("Shift+Tab did not toggle Plan mode outside dashboard")
	}
}

func TestSettingsPromotionMovesWorkspaceValueToGlobal(t *testing.T) {
	settings := newTestSettings(t)
	const value = "321"
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyReserveTokens, value); err != nil {
		t.Fatalf("seed workspace setting: %v", err)
	}

	harness := tui.NewSettingsPromotionHarness(t.Context(), settings, prefs.KeyReserveTokens)
	harness.Press(tea.KeyPressMsg{Code: 'p', Text: "p"})

	global, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyReserveTokens)
	if err != nil {
		t.Fatalf("read global setting: %v", err)
	}
	if global.Value != value {
		t.Fatalf("global value = %q, want %q", global.Value, value)
	}
	if _, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyReserveTokens); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("workspace row still exists or returned wrong error: %v", err)
	}
}

func TestSettingsCtrlGOpensAndClosesContextualHelpWithoutMutation(t *testing.T) {
	settings := newTestSettings(t)
	const value = "654"
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyReserveTokens, value); err != nil {
		t.Fatalf("seed workspace setting: %v", err)
	}
	harness := tui.NewSettingsPromotionHarness(t.Context(), settings, prefs.KeyReserveTokens)
	wantCategory, wantRow, wantFocused := harness.Focus()

	harness.Press(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !harness.HelpOpen() {
		t.Fatal("Ctrl+G did not open contextual help above settings")
	}
	if help := harness.HelpText(); !strings.Contains(help, "move workspace value to global") {
		t.Fatalf("Ctrl+G help = %q, want Settings controls", help)
	}
	gotCategory, gotRow, gotFocused := harness.Focus()
	if gotCategory != wantCategory || gotRow != wantRow || gotFocused != wantFocused {
		t.Fatalf("Ctrl+G changed settings focus to (%d,%d,%v), want (%d,%d,%v)",
			gotCategory, gotRow, gotFocused, wantCategory, wantRow, wantFocused)
	}
	workspace, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyReserveTokens)
	if err != nil || workspace.Value != value {
		t.Fatalf("workspace setting after Ctrl+G = %#v, %v", workspace, err)
	}
	if _, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyReserveTokens); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("Ctrl+G promoted settings value: %v", err)
	}

	harness.Press(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if harness.HelpOpen() || !harness.SettingsTopmost() {
		t.Fatal("Ctrl+G on topmost help did not close back to settings")
	}
}
