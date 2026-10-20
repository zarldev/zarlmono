package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func assertTranscriptBuffer(t *testing.T, canvas uv.ScreenBuffer, row, width int) {
	t.Helper()
	for x := range width - 1 { // The scrollbar owns the final column.
		cell := canvas.CellAt(x, row)
		if strings.TrimSpace(cell.Content) != "" || !sameSurface(cell.Style.Bg, lg.Color(string(theme.DarkDefault().Bg))) {
			t.Fatalf("buffer cell (%d,%d) = %+v, want blank neutral transcript content", x, row, cell)
		}
	}
}

func TestTranscriptTrailingBufferFollowsContent(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, lines := range []int{1, 80} {
		for _, draft := range []string{"", "one\ntwo\nthree"} {
			t.Run(fmt.Sprintf("lines=%d/multiline=%t", lines, draft != ""), func(t *testing.T) {
				ui := tui.New()
				ui.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
				ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("history\n", lines-1) + "FINAL_ENTRY"}})
				if draft != "" {
					ui.Update(tea.PasteMsg{Content: draft})
				}
				for _, width := range []int{80, 60} {
					ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
					canvas := surfaceCanvas(ui, width, 30)
					last := surfaceRow(t, canvas, "FINAL_ENTRY")
					// User entries already have one role-colored closing row. The
					// new neutral buffer follows that row, without replacing it.
					assertTranscriptBuffer(t, canvas, last+2, width)
					if lines > 1 {
						wantLast := 24
						if draft != "" {
							wantLast -= 2
						}
						if last != wantLast {
							t.Fatalf("last entry row = %d, want %d (role closing row and neutral buffer before composer separator)", last, wantLast)
						}
						ui.Update(tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelUp})
						browsing := surfaceCanvas(ui, width, 30)
						if !strings.Contains(transcriptScreenRow(browsing, last+2), "history") {
							t.Fatal("buffer stayed fixed at viewport bottom instead of scrolling with the final entry")
						}
						// Keyboard selection remains in browse mode at the bottom.
						ui.Update(tea.KeyPressMsg{Code: '\t'})
						ui.Update(tea.KeyPressMsg{Code: 'v'})
						ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
						assertTranscriptBuffer(t, surfaceCanvas(ui, width, 30), last+2, width)
						ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
						ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
						ui.Update(tea.KeyPressMsg{Code: '\t'})
					}
				}
			})
		}
	}
}

func TestTranscriptTrailingBufferAfterTools(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "buffer", Prompt: "inspect"})
	ui.Update(teasink.ContentMsg{TaskID: "buffer", Delta: "ANSWER"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "buffer", ToolID: "call", ExecutionID: "call", ToolName: "inspect", Parameters: map[string]any{"path": "BODY"}})
	check := func() {
		t.Helper()
		canvas := surfaceCanvas(ui, 100, 40)
		last := 2
		for y := 2; y < 37; y++ {
			if strings.TrimSpace(screenRegion(canvas, 0, y, 99, y+1)) != "" {
				last = y
			}
		}
		assertTranscriptBuffer(t, canvas, last+1, 100)
		before := screenRegion(canvas, 0, 2, 99, 37)
		ui.Update(tea.MouseClickMsg{X: 3, Y: last + 1, Button: tea.MouseLeft})
		ui.Update(tea.MouseClickMsg{X: 0, Y: last + 1, Button: tea.MouseLeft})
		if after := screenRegion(surfaceCanvas(ui, 100, 40), 0, 2, 99, 37); after != before {
			t.Fatal("clicking trailing padding selected or toggled an item")
		}
	}
	check()
	clickToolLabel(t, ui, "[+] tools")
	clickToolLabel(t, ui, "[+] ◌ inspect")
	check()
	ui.Update(teasink.ToolCompletedMsg{TaskID: "buffer", ToolID: "call", ExecutionID: "call", ToolName: "inspect", FormattedResult: "FINAL_RESULT"})
	check()
	clickToolLabel(t, ui, "[-] tools")
	check()
}

func TestTranscriptTrailingBufferDoesNotCopy(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "COPY_ME"}})
	_ = ui.View()
	ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	ui.Update(tea.KeyPressMsg{Code: '\t'})
	ui.Update(tea.KeyPressMsg{Code: 'v'})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd == nil {
		t.Fatal("copy returned no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatal("copy returned no clipboard batch")
	}
	if got := fmt.Sprint(batch[0]()); got != "COPY_ME" {
		t.Fatalf("copy included trailing padding: %q", got)
	}
}
