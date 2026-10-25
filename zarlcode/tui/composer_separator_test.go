package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestComposerSeparatorAndInsetPreserveFeedbackRow(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, width := range []int{20, 60, 220} {
		for _, plan := range []bool{false, true} {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			ui.SetPlanMode(plan)
			ui.Update(tea.PasteMsg{Content: "INPUT\nNEXT"})
			canvas := surfaceCanvas(ui, width, 30)
			row := surfaceRow(t, canvas, "INPUT")
			if row != 27 || canvas.CellAt(0, row).Content != "›" || canvas.CellAt(2, row).Content != "I" || canvas.CellAt(2, row+1).Content != "N" {
				t.Fatal("input does not share transcript inset")
			}
			if got := transcriptScreenRow(canvas, row-1); got != strings.Repeat("─", width) {
				t.Fatalf("separator = %q", got)
			}
			for _, y := range []int{row, row + 1, 28} {
				line := transcriptScreenRow(canvas, y)
				if strings.ContainsAny(line, "│┌┐└┘") {
					t.Fatal("composer still has perimeter rails")
				}
			}
			if !strings.Contains(transcriptScreenRow(canvas, 28), "NEXT") {
				t.Fatal("last input row must sit directly above feedback")
			}
			ui.SetErrorToast("warning")
			canvas = surfaceCanvas(ui, width, 30)
			if surfaceRow(t, canvas, "INPUT") != row || !strings.Contains(transcriptScreenRow(canvas, 29), "warning") {
				t.Fatal("feedback shifted or obscured input")
			}
		}
	}
}

func TestComposerSeparatorRetainsCursorScrollAndWideInput(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	ui.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet})
	text := "FIRST\nsecond\nthird\nfourth\nfifth\nsixth\nseventh\n界 é LAST"
	ui.Update(tea.PasteMsg{Content: text})
	canvas := surfaceCanvas(ui, 40, 30)
	row := surfaceRow(t, canvas, "LAST")
	if canvas.CellAt(2, row).Content != "界" || canvas.CellAt(2, row).Width != 2 || !strings.Contains(transcriptScreenRow(canvas, row), "▏") {
		t.Fatal("wide input or cursor lost at scroll tail")
	}
	for range len([]rune(text)) {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	canvas = surfaceCanvas(ui, 40, 30)
	row = surfaceRow(t, canvas, "FIRST")
	if canvas.CellAt(2, row).Content != "▏" {
		t.Fatal("cursor scroll did not return to first input row")
	}
	if ui.ComposerText() != text {
		t.Fatal("rendering changed authored input")
	}
}
