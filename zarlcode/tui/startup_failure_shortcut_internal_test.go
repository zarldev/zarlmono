package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestStartupFailureCtrlSOpensSettings(t *testing.T) {
	m := tui.New()
	m.SetWorkspace("/home/bruno", "")
	m.SetSettings(&engine.Settings{})
	m.SetStartupFailure("/home/bruno", "provider startup", "boom")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = mm.(*tui.UI)
	mm, _ = m.Update(tea.KeyPressMsg{Code: 's', Text: "s", Mod: tea.ModCtrl})
	m = mm.(*tui.UI)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Settings") && !strings.Contains(view, "settings") {
		t.Fatalf("ctrl+s should open settings overlay, got view:\n%s", view)
	}
}
