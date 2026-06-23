package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestStartupFailurePaneRenders(t *testing.T) {
	m := tui.New()
	m.SetWorkspace("/home/bruno", "")
	m.SetStartupFailure("/home/bruno", "provider startup", `provider "anthropic": invalid api key`)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = mm.(*tui.UI)

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"startup failed", "provider startup", `provider "anthropic": invalid api key`, "workspace", "ctrl+s settings"} {
		if !strings.Contains(out, want) {
			t.Fatalf("startup failure screen missing %q:\n%s", want, out)
		}
	}
}

func TestStartupFailurePaneQuitsOnEnter(t *testing.T) {
	m := tui.New()
	m.SetStartupFailure("/home/bruno", "provider startup", "boom")
	mm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(*tui.UI)
	if cmd == nil {
		t.Fatal("enter should return quit command")
	}
	if m == nil {
		t.Fatal("model should remain valid")
	}
}
