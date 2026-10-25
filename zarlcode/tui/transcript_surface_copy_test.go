package tui_test

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestRailFreeCopyPreservesAuthoredIndentationAndPipes(t *testing.T) {
	const source = "  │ user indentation\n| literal pipe\nyou\nagent"
	for _, selection := range []bool{false, true} {
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: source}})
		_ = ui.View()
		ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		ui.Update(tea.KeyPressMsg{Code: '\t'})
		key := 'y'
		if selection {
			ui.Update(tea.KeyPressMsg{Code: 'v'})
			for range 3 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
		} else {
			ui.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
			_ = ui.View()
			key = 'Y'
		}
		_, cmd := ui.Update(tea.KeyPressMsg{Code: key})
		if cmd == nil {
			t.Fatal("copy returned no command")
		}
		batch, ok := cmd().(tea.BatchMsg)
		if !ok || len(batch) == 0 {
			t.Fatal("copy did not produce clipboard batch")
		}
		// Executing the clipboard command only constructs the Bubble Tea message;
		// it performs no terminal I/O and does not touch the system clipboard.
		if got := fmt.Sprint(batch[0]()); got != source {
			t.Fatalf("selection=%t: copy = %q, want %q", selection, got, source)
		}
	}
}

func TestTranscriptSurfacesPreserveWideCellsAndSelection(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	ui.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet})
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "界 e\u0301 wide\nnext line"}})
	canvas := surfaceCanvas(ui, 60, 30)
	row := surfaceRow(t, canvas, "界")
	first, next := canvas.CellAt(2, row), canvas.CellAt(3, row)
	if first.Content != "界" || first.Width != 2 || next.Width != 0 {
		t.Fatalf("wide glyph damaged: %v %v", first, next)
	}
	if !sameSurface(first.Style.Bg, color.RGBA{33, 41, 58, 255}) {
		t.Fatal("wide glyph lost surface")
	}
	ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	ui.Update(tea.KeyPressMsg{Code: '\t'})
	ui.Update(tea.KeyPressMsg{Code: 'v'})
	canvas = surfaceCanvas(ui, 60, 30)
	selected := canvas.CellAt(0, row).Style.Bg
	if selected == nil || sameSurface(selected, color.RGBA{33, 41, 58, 255}) {
		t.Fatal("selection did not override role background")
	}
	for x := range 59 {
		c := canvas.CellAt(x, row)
		if c.Width > 0 && !sameSurface(c.Style.Bg, selected) {
			t.Fatalf("selection stripe at x=%d", x)
		}
	}
	if !strings.Contains(ansi.Strip(ui.View().Content), "界 é wide") {
		t.Fatal("selection shifted or damaged text")
	}
}
