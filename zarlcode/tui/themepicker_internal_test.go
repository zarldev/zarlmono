package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestThemePicker_StartsAtCurrent(t *testing.T) {
	UseTheme(theme.Theme{Name: "nord"})
	defer UseTheme(theme.Theme{})

	p := newThemePicker()
	if len(p.names) == 0 {
		t.Fatal("picker has no themes")
	}
	if p.names[p.cursor] != "nord" {
		t.Errorf("cursor should start at the current theme, got %q", p.names[p.cursor])
	}
}

func TestHandleAction_SetThemeSwitchesAndCloses(t *testing.T) {
	UseTheme(theme.Theme{Name: "placeholder"})
	defer UseTheme(theme.Theme{})

	m := New()
	m.overlay.push(newThemePicker())
	m.handleAction(actionSetTheme{name: "nord"})

	if palette.Name != "nord" {
		t.Errorf("theme not switched: palette=%q", palette.Name)
	}
	if m.overlay.active() {
		t.Error("picker should close after selecting a theme")
	}
}

func TestUI_CtrlTOpensThemePicker(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 40})
	step(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 't'})
	if !m.overlay.active() {
		t.Fatal("ctrl+t should open the theme picker")
	}
}
