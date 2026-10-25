package tui_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestSidebarNavigationIsIndependent(t *testing.T) {
	ui := sidebarFixture(160, 20)
	steps := make([]code.PlanStep, 30)
	for i := range steps {
		steps[i] = code.PlanStep{Text: fmt.Sprintf("Retained step %02d", i), Status: code.StepStatuses.PENDING}
	}
	ui.SetPlan(code.Plan{Steps: steps})
	ui.Update(tea.PasteMsg{Content: "draft"})
	initial := sidebarText(ui, 160, 20)
	transcript := screenRegion(surfaceCanvas(ui, 160, 20), 0, 0, 104, 20)
	for _, key := range []rune{tea.KeyDown, tea.KeyPgDown, tea.KeyEnd, tea.KeyPgUp, tea.KeyUp, tea.KeyHome} {
		ui.Update(tea.KeyPressMsg{Code: key, Mod: tea.ModAlt})
		if got := screenRegion(surfaceCanvas(ui, 160, 20), 0, 0, 104, 20); got != transcript {
			t.Fatalf("alt sidebar key %d changed transcript or composer", key)
		}
	}
	if got := sidebarText(ui, 160, 20); got != initial {
		t.Fatal("alt+home did not return to first sidebar row")
	}
	ui.Update(tea.MouseWheelMsg{X: 110, Y: 5, Button: tea.MouseWheelDown})
	scrolled := sidebarText(ui, 160, 20)
	if scrolled == initial {
		t.Fatal("sidebar wheel did not scroll")
	}
	if got := screenRegion(surfaceCanvas(ui, 160, 20), 0, 0, 104, 20); got != transcript {
		t.Fatal("sidebar wheel scrolled transcript or changed composer")
	}
	for _, overlay := range []rune{'p', 'l'} {
		ui.Update(tea.KeyPressMsg{Code: overlay, Mod: tea.ModCtrl})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
		ui.Update(tea.MouseWheelMsg{X: 110, Y: 5, Button: tea.MouseWheelDown})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if got := sidebarText(ui, 160, 20); got != scrolled {
			t.Fatalf("ctrl+%c overlay let navigation reach sidebar", overlay)
		}
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	if got := sidebarText(ui, 160, 20); !strings.Contains(got, "Retained step 29") {
		t.Fatalf("alt+end did not expose final step:\n%s", got)
	}
	updated := slices.Clone(steps)
	updated[29].Status = code.StepStatuses.COMPLETED
	ui.Update(teasink.PlanUpdatedMsg{TaskID: "summary", Plan: code.Plan{Steps: updated}})
	if got := sidebarText(ui, 160, 20); !strings.Contains(got, "✓ Retained step 29") {
		t.Fatalf("status update reset scroll or retained cached status:\n%s", got)
	}
	ui.Update(teasink.PlanUpdatedMsg{TaskID: "summary", Plan: code.Plan{Steps: []code.PlanStep{{Text: "Replacement work", Status: code.StepStatuses.INPROGRESS}}}})
	if got := sidebarText(ui, 160, 20); !strings.Contains(got, "synthetic-model") || !strings.Contains(got, "Replacement work") {
		t.Fatalf("replacement did not reset scroll/cache:\n%s", got)
	}
	ui.SetPlan(code.Plan{})
	if got := sidebarText(ui, 160, 20); strings.Contains(got, "Replacement work") {
		t.Fatal("cleared plan retained cached rows")
	}
}

func TestSidebarPlanCacheRewrapsAndRecolors(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := sidebarFixture(160, 40)
	plan := code.Plan{Steps: []code.PlanStep{{Text: "CaseSensitiveAPI " + strings.Repeat("日本語 detail ", 15) + "FINAL_TOKEN", Status: code.StepStatuses.INPROGRESS}}, Explanation: "old explanation"}
	ui.SetPlan(plan)
	_ = ui.View()
	// Reusing and mutating the source slice must not alias the cached snapshot.
	plan.Steps[0].Text = "RevisedAPI " + strings.Repeat("日本語 detail ", 15) + "FINAL_TOKEN"
	plan.Explanation = "updated"
	ui.SetPlan(plan)
	for _, size := range [][2]int{{120, 14}, {160, 40}, {120, 8}, {160, 60}} {
		ui.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		text := collectSidebar(ui, size[0], size[1])
		for _, want := range []string{"RevisedAPI", "FINAL_TOKEN", "updated"} {
			// At one-row sidebar height the explanation is still reachable,
			// though its label may wrap onto its own row.
			if !strings.Contains(text, want) {
				t.Fatalf("size %v lost %q:\n%s", size, want, text)
			}
		}
		if strings.Contains(text, "CaseSensitiveAPI") || strings.Contains(text, "old explanation") {
			t.Fatal("sidebar retained old cached text")
		}
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModAlt})
	changed := theme.DarkDefault()
	changed.Fg = "#abcdef"
	tui.UseTheme(changed)
	canvas := surfaceCanvas(ui, 160, 60)
	row := surfaceRow(t, canvas, "RevisedAPI")
	if !sameSurface(canvas.CellAt(108, row).Style.Fg, lg.Color("#abcdef")) {
		t.Fatal("cached active step retained old theme")
	}
}
