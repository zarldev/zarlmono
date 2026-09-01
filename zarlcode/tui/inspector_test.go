package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestInspectorOpensWithDocumentedShortcut(t *testing.T) {
	ui := tui.New()
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	ui = model.(*tui.UI)
	model, _ = ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	ui = model.(*tui.UI)
	out := ansi.Strip(ui.View().Content)
	for _, want := range []string{"inspector", "tools", "prompt", "runtime", "processes", "mcp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspector missing %q:\n%s", want, out)
		}
	}
}

func TestEventRingRetainsNewestEntries(t *testing.T) {
	ring := tui.NewEventRing(2)
	ring.Add(tui.EventRingEntry{Kind: "a"})
	ring.Add(tui.EventRingEntry{Kind: "b"})
	ring.Add(tui.EventRingEntry{Kind: "c"})
	got := ring.Snapshot()
	if len(got) != 2 || got[0].Kind != "b" || got[1].Kind != "c" {
		t.Fatalf("snapshot = %#v", got)
	}
}
