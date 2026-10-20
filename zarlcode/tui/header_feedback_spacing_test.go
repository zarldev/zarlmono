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

func TestActivitySeparatorHasNoReservedBlankRowAbove(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, width := range []int{40, 220} {
		for _, draft := range []string{"", "one\ntwo\nthree"} {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("history\n", 80)}})
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
			if draft != "" && !strings.Contains(transcriptScreenRow(canvas, 28), "three") {
				t.Fatal("removing the gap moved the input")
			}
			// Browse into the uninterrupted history body, away from legitimate
			// transcript padding at the tail and between turns.
			for range 5 {
				ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
			}
			canvas = surfaceCanvas(ui, width, 30)
			if !strings.Contains(transcriptScreenRow(canvas, separator-1), "history") {
				t.Fatalf("width %d draft %q: reserved blank row remains above input", width, draft)
			}
			if bg := canvas.CellAt(2, separator-1).Style.Bg; bg == nil || !sameSurface(bg, canvas.CellAt(2, separator-2).Style.Bg) {
				t.Fatal("reclaimed history row lost its transcript surface")
			}
			if !strings.Contains(transcriptScreenRow(canvas, separator), "0:00") || ui.ComposerText() != draft {
				t.Fatal("browsing changed the activity row or authored input")
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
