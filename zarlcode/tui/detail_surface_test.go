package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func detailTextPosition(t *testing.T, canvas uv.ScreenBuffer, text string, y0, y1 int) (int, int) {
	t.Helper()
	for y := y0; y < y1; y++ {
		line := screenRegion(canvas, 0, y, canvas.Bounds().Dx(), y+1)
		if index := strings.Index(line, text); index >= 0 {
			return ansi.StringWidth(line[:index]), y
		}
	}
	t.Fatalf("missing %q in rows %d..%d:\n%s", text, y0, y1, canvas.String())
	return 0, 0
}

func assertDetailChrome(t *testing.T, canvas uv.ScreenBuffer, title, active string) {
	t.Helper()
	width, height := canvas.Bounds().Dx(), canvas.Bounds().Dy()
	x, y := detailTextPosition(t, canvas, title, 0, 1)
	if x != 2 || canvas.CellAt(x, y).Style.Attrs&uv.AttrBold == 0 {
		t.Errorf("%s title must be bold at the two-cell inset", title)
	}
	if !sameSurface(canvas.CellAt(x, y).Style.Bg, canvas.CellAt(0, 0).Style.Bg) {
		t.Error("detail title uses a different surface")
	}
	x, y = detailTextPosition(t, canvas, active, 0, 1)
	if canvas.CellAt(x, y).Style.Attrs&uv.AttrBold == 0 {
		t.Errorf("active %s tab is not emphasized", active)
	}
	// The context gauge's ┤ threshold marker is data, not a frame junction.
	if strings.ContainsAny(screenRegion(canvas, 0, 0, width, height), "┌┐└┘├") {
		t.Error("detail view retained nested frame chrome")
	}
}

func TestDetailViewsShareMainSurface(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, name := range []string{"midnight", "parchment", "dracula"} {
		th, _ := theme.ByName(name)
		tui.UseTheme(th)
		for _, width := range []int{52, 80, 160} {
			t.Run(fmt.Sprintf("%s/%d", name, width), func(t *testing.T) {
				const height = 28
				ui := sidebarFixture(width, height)
				ui.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
				for _, tab := range []string{"overview", "context", "prompt", "tools", "events"} {
					canvas := surfaceCanvas(ui, width, height)
					assertDetailChrome(t, canvas, "session", tab)
					if tab != "overview" {
						x, y := detailTextPosition(t, canvas, tab, 2, height-5)
						if x != 2 || canvas.CellAt(x, y).Style.Attrs&uv.AttrBold == 0 {
							t.Errorf("%s section heading is not aligned/bold", tab)
						}
					}
					detailTextPosition(t, canvas, "esc close", height-5, height-4)
					ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
				}
				ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				canvas := surfaceCanvas(ui, width, height)
				assertDetailChrome(t, canvas, "plan", "live")
				x, y := detailTextPosition(t, canvas, "Current refinement", 2, height-1)
				if canvas.CellAt(x, y).Style.Attrs&uv.AttrBold == 0 {
					t.Error("current plan step is not emphasized")
				}
				x, y = detailTextPosition(t, canvas, "Finished investigation", 2, height-1)
				if canvas.CellAt(x, y).Style.Attrs&uv.AttrBold != 0 {
					t.Error("completed work competes with the current step")
				}
				detailTextPosition(t, canvas, "tab saved plans", height-1, height)
				ui.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "Updated live step", Status: code.StepStatuses.INPROGRESS}}})
				detailTextPosition(t, surfaceCanvas(ui, width, height), "Updated live step", 2, height-1)
				ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				if strings.Contains(ansi.Strip(ui.View().Content), "plan  live  saved") {
					t.Error("Ctrl+P did not close the plan reader")
				}
			})
		}
	}
}

func TestPlanDetailWrappedScrollAndResize(t *testing.T) {
	ui := tui.New()
	steps := make([]code.PlanStep, 30)
	for i := range steps {
		steps[i] = code.PlanStep{Text: fmt.Sprintf("Step %d 日本語 %s END-%d", i, strings.Repeat("long description ", 40), i), Status: code.StepStatuses.PENDING}
	}
	ui.SetPlan(code.Plan{Steps: steps, Explanation: "Final explanation sentinel"})
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, width := range []int{120, 36, 52, 160} {
		ui.Update(tea.WindowSizeMsg{Width: width, Height: 18})
		_ = surfaceCanvas(ui, width, 18)
		ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		for range 40 {
			ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		bottom := surfaceCanvas(ui, width, 18)
		text := strings.Join(strings.Fields(screenRegion(bottom, 2, 2, width-2, 17)), "")
		for _, want := range []string{"END-29", "Finalexplanationsentinel"} {
			if !strings.Contains(text, want) {
				t.Errorf("width %d: End lost %s:\n%s", width, want, bottom.String())
			}
		}
		ui.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		for range 180 {
			ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		if got := surfaceCanvas(ui, width, 18).String(); got != bottom.String() {
			t.Errorf("width %d: returning to final step and paging down changed detail bottom", width)
		}
		ui.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
		if got := surfaceCanvas(ui, width, 18).String(); got == bottom.String() {
			t.Errorf("width %d: page-up did not move", width)
		}
	}
}

func TestSavedPlanDetailResponsiveNavigation(t *testing.T) {
	dir := t.TempDir()
	plans := filepath.Join(dir, code.PlansDir)
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		content := "# " + name + "\n\n" + strings.Repeat("Long saved plan paragraph with 日本語.\n\n", 40) + name + "-END"
		if err := os.WriteFile(filepath.Join(plans, name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, width := range []int{36, 52, 80, 160} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			const height = 20
			ui := tui.New()
			ui.SetWorkspace(dir, "")
			ui.Update(tea.WindowSizeMsg{Width: width, Height: height})
			ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			canvas := surfaceCanvas(ui, width, height)
			assertDetailChrome(t, canvas, "plan", "saved")
			detailTextPosition(t, canvas, "source: saved plan markdown", 2, height-1)
			detailTextPosition(t, canvas, "esc close", height-1, height)
			first := canvas.String()
			ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			canvas = surfaceCanvas(ui, width, height)
			if canvas.String() == first {
				t.Error("saved selection did not change")
			}
			for range 100 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			}
			if got := surfaceCanvas(ui, width, height).String(); !strings.Contains(got, "-END") {
				t.Errorf("saved preview bottom unreachable:\n%s", got)
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyHome})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			if got := surfaceCanvas(ui, width, height).String(); got != first {
				t.Error("saved list/preview did not return to initial position")
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			assertDetailChrome(t, surfaceCanvas(ui, width, height), "plan", "live")
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if strings.Contains(ansi.Strip(ui.View().Content), "plan  live  saved") {
				t.Error("escape did not close the plan reader")
			}
		})
	}
}
