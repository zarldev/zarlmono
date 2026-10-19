package tui_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// assertComposerMode observes the retained mode cue rather than relying on the
// removed footer text or adding a test-only mode-state accessor.
func assertComposerMode(t *testing.T, ui *tui.UI, plan bool) {
	t.Helper()
	palette := theme.DarkDefault()
	tui.UseTheme(palette)
	t.Cleanup(func() { tui.UseTheme(theme.Theme{}) })
	ui.Update(tea.WindowSizeMsg{Width: 220, Height: 50})
	canvas := surfaceCanvas(ui, 220, 50)
	want := palette.BorderFocus
	if plan {
		want = palette.PlanMode
	}
	if !sameSurface(canvas.CellAt(0, 47).Style.Fg, lg.Color(string(want))) {
		t.Fatalf("composer colour did not reflect applied plan=%t", plan)
	}
}

func TestStatusErrorAndSlashGuidanceRemainVisibleWithoutModeLabel(t *testing.T) {
	for _, plan := range []bool{false, true} {
		ui := tui.New()
		ui.SetPlanMode(plan)
		step(t, ui, textKey("/"))
		ui.SetErrorToast("provider unavailable")
		wide := renderStatus(t, ui, 220)
		if !strings.Contains(wide, "slash commands") || !strings.Contains(wide, "provider unavailable") {
			t.Fatalf("lost guidance or error: %q", wide)
		}
		narrow := renderStatus(t, ui, 20)
		if !strings.Contains(narrow, "provider") || strings.Contains(narrow, "slash commands") {
			t.Fatalf("narrow error did not take precedence: %q", narrow)
		}
	}
}

func TestStatusReturnsToQuietAfterTransientFeedback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := tui.New()
		ui.SetErrorToast("temporary warning")
		if !strings.Contains(renderStatus(t, ui, 80), "temporary warning") {
			t.Fatal("toast missing")
		}
		time.Sleep(5 * time.Second)
		if got := strings.TrimSpace(renderStatus(t, ui, 80)); got != "" {
			t.Fatalf("expired feedback left footer text %q", got)
		}
	})
}
