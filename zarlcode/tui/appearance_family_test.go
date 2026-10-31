package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func familySettingsUI(t *testing.T) (*tui.UI, *engine.Settings, *appearance.Store, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	state, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(); tui.UseTheme(theme.Theme{}) })
	settings := engine.NewSettings(state, nil, nil, workspace)
	m := tui.New()
	m.SetWorkspace(workspace, "fixture-model")
	m.SetSettings(settings)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	path, err := appearance.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	return m, settings, appearance.New(path), workspace, path
}

func openFamilyPicker(t *testing.T, m *tui.UI, expected string) {
	t.Helper()
	m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, expected) {
		t.Fatalf("picker missing %q:\n%s", expected, got)
	}
}

func closeFamilyPicker(m *tui.UI) { m.Update(tea.KeyPressMsg{Code: tea.KeyEsc}) }

func TestFamilyAppearancePreservesLegacySpecificityAndInheritance(t *testing.T) {
	m, settings, family, workspace, _ := familySettingsUI(t)
	ctx := t.Context()
	if err := family.Set(ctx, appearance.Global(), "retro-amber"); err != nil {
		t.Fatal(err)
	}
	check := func(expected string) {
		t.Helper()
		openFamilyPicker(t, m, expected)
		closeFamilyPicker(m)
	}
	check("effective: retro-amber (global)")
	if err := settings.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyTheme, "terminal-green"); err != nil {
		t.Fatal(err)
	}
	check("effective: terminal-green (app)")
	if err := settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTheme, "catppuccin-mocha"); err != nil {
		t.Fatal(err)
	}
	if err := family.Set(ctx, appearance.App("zarlcode"), "synthwave"); err != nil {
		t.Fatal(err)
	}
	check("effective: catppuccin-mocha (workspace)")
	if err := family.Set(ctx, appearance.Workspace("zarlcode", workspace), "ansi-bbs"); err != nil {
		t.Fatal(err)
	}
	openFamilyPicker(t, m, "effective: ansi-bbs (workspace)")
	m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if _, err := settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTheme); err == nil {
		t.Fatal("inherit resurrected explicit legacy workspace selection")
	}
	check("effective: synthwave (app)")
	openFamilyPicker(t, m, "target: workspace")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // global
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // app
	m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	check("effective: retro-amber (global)")
}

func TestFamilyPickerCancelRestoresUnregisteredPalette(t *testing.T) {
	m, _, _, _, _ := familySettingsUI(t)
	tui.UseTheme(theme.Theme{Name: "live-custom", Fg: "#ddeeff", Primary: "#abcdef", Border: "#445566"})
	before := m.View().Content
	openFamilyPicker(t, m, "theme appearance")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	closeFamilyPicker(m)
	if m.View().Content != before {
		t.Fatal("cancel replaced the exact live palette with a catalogue lookup")
	}
}

func TestFailedFamilyResetDoesNotDeleteLegacySelection(t *testing.T) {
	m, settings, family, _, path := familySettingsUI(t)
	ctx := t.Context()
	if err := settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTheme, "terminal-green"); err != nil {
		t.Fatal(err)
	}
	if err := family.Set(ctx, appearance.Global(), "retro-amber"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	openFamilyPicker(t, m, "theme appearance")
	m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	value, err := settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTheme)
	if err != nil || value.Value != "terminal-green" {
		t.Fatalf("failed reset lost preference: %+v / %v", value, err)
	}
	closeFamilyPicker(m)
}
