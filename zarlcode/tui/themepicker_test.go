package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestThemePickerStartsAtCurrentAndPreviews(t *testing.T) {
	current, ok := theme.ByName("nord")
	if !ok {
		t.Skip("nord theme unavailable")
	}
	tui.UseTheme(current)
	defer tui.UseTheme(theme.Theme{})
	m := tui.New()
	m.OpenThemePicker()
	step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	out := m.View().Content
	if !strings.Contains(out, "▸ nord") {
		t.Fatalf("picker did not start at current theme:\n%s", out)
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.View().Content == out {
		t.Fatal("down should move theme selection")
	}
}

func TestUICtrlTOpensThemePicker(t *testing.T) {
	m := tui.New()
	step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	step(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 't'})
	if !strings.Contains(m.View().Content, "themes") {
		t.Fatal("ctrl+t should open theme picker")
	}
}
