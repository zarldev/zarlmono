package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestActivitySeparatorHasOneBlankRowAbove(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, width := range []int{40, 220} {
		for _, draft := range []string{"", "one\ntwo\nthree"} {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			messages := make([]llm.Message, 30)
			for i := range messages {
				messages[i] = llm.Message{Role: llm.RoleUser, Content: "history"}
			}
			ui.AddTranscriptMessages(messages)
			ui.Update(teasink.ConversationStartedMsg{TaskID: "padding", Prompt: "working"})
			if draft != "" {
				ui.Update(tea.PasteMsg{Content: draft})
			}
			canvas := surfaceCanvas(ui, width, 30)
			separator := 27
			if draft != "" {
				separator = 25
			}
			if !strings.Contains(transcriptScreenRow(canvas, separator), "0:00") {
				t.Fatal("input activity moved from its existing row")
			}
			if strings.TrimSpace(transcriptScreenRow(canvas, separator-1)) != "" {
				t.Fatal("activity separator lacks blank row above")
			}
			if canvas.CellAt(2, separator-1).Style.Bg != nil {
				t.Fatal("spacer inherited transcript role surface")
			}
			if draft != "" && !strings.Contains(transcriptScreenRow(canvas, 28), "three") {
				t.Fatal("padding raised the input")
			}
			ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
			if strings.TrimSpace(transcriptScreenRow(surfaceCanvas(ui, width, 30), separator-1)) != "" {
				t.Fatal("browse rendering filled the spacer")
			}
		}
	}
}

func TestSettlementFeedbackSeverityAndStableInset(t *testing.T) {
	palette := theme.DarkDefault()
	tui.UseTheme(palette)
	defer tui.UseTheme(theme.Theme{})
	queued := newBeforeFixture(t)
	ui := queued.ui
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	queued.live.QueueInput("queued")
	canvas := surfaceCanvas(ui, 100, 30)
	row := surfaceRow(t, canvas, "Queued input ready")
	if strings.Index(transcriptScreenRow(canvas, row), "Queued input ready") != 3 {
		t.Fatal("settlement feedback moved from common inset")
	}
	if !sameSurface(canvas.CellAt(3, row).Style.Fg, lg.Color(string(palette.Subtle))) {
		t.Fatal("queued progress is styled as an error")
	}
	f, _ := failedRecoveryFixture(t)
	f.ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	canvas = surfaceCanvas(f.ui, 100, 30)
	row = surfaceRow(t, canvas, "Not saved")
	if !sameSurface(canvas.CellAt(3, row).Style.Fg, lg.Color(string(palette.Error))) {
		t.Fatal("save failure lost error severity")
	}
	if strings.Index(transcriptScreenRow(canvas, row), "Not saved") != 3 {
		t.Fatal("error moved from common inset")
	}
}
