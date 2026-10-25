package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
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

func TestPlanInspectorShowsAdvertisedToolsAndDispatchRestrictions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, workspace, "fixture")
	t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })
	live.SetPlanMode(true)
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetPlanMode(true)
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 200})
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	out := ansi.Strip(ui.View().Content)
	for _, want := range []string{"advertised", "bash", "edit (restricted in PLAN)", "write (restricted in PLAN)"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspector missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"(hidden)", "bash (restricted in PLAN)"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("inspector mislabeled tools with %q", unwanted)
		}
	}
}
