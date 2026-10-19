package tui_test

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestSidebarPolishHierarchyAndSpacing(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, name := range []string{"midnight", "parchment", "dracula"} {
		th, _ := theme.ByName(name)
		tui.UseTheme(th)
		for _, width := range []int{120, 160} {
			for _, height := range []int{20, 40} {
				t.Run(fmt.Sprintf("%s/%dx%d", name, width, height), func(t *testing.T) {
					ui := sidebarFixture(width, height)
					ui.SetWorkspace("/synthetic/workspace", "synthetic-model")
					canvas := surfaceCanvas(ui, width, height)
					sideWidth := 32
					if width >= 160 {
						sideWidth = 56
					}
					x := width - sideWidth + 2
					row := func(text string) int {
						t.Helper()
						for y := range height - 4 {
							if strings.Contains(screenRegion(canvas, x, y, width, y+1), text) {
								return y
							}
						}
						t.Fatalf("sidebar missing %q", text)
						return -1
					}
					if row("synthetic-model") != 1 || row("openai") != 2 {
						t.Error("identity is not grouped immediately below session heading")
					}
					planY := row("1/3 complete")
					activeY := row("Current refinement")
					if canvas.CellAt(x, planY).Style.Attrs&uv.AttrBold == 0 || canvas.CellAt(x+2, activeY).Style.Attrs&uv.AttrBold == 0 {
						t.Error("plan heading and current step must be bold")
					}
					progress := canvas.CellAt(x+7, planY)
					footer := canvas.CellAt(x, row("ctrl+l more"))
					if progress.Style.Attrs&uv.AttrBold != 0 || !sameSurface(progress.Style.Fg, footer.Style.Fg) {
						t.Error("progress should use quiet, non-bold secondary text")
					}
					gap := 1
					if height == 40 {
						gap = 2
						next := canvas.CellAt(x, row("☐ Verify rendering"))
						if next.Style.Attrs&uv.AttrBold != 0 || sameSurface(next.Style.Fg, canvas.CellAt(x+2, activeY).Style.Fg) {
							t.Error("next step competes with current work")
						}
						row("/synthetic/workspace")
					}
					if got := row("context") - row("openai") - 1; got != gap {
						t.Errorf("context gap = %d, want %d", got, gap)
					}
					for y := planY - gap; y < planY; y++ {
						if strings.TrimSpace(screenRegion(canvas, x, y, width, y+1)) != "" {
							t.Error("plan spacing consumed by content")
						}
					}

					ui.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "Pending work", Status: code.StepStatuses.PENDING}}})
					canvas = surfaceCanvas(ui, width, height)
					if canvas.CellAt(x, row("☐ Pending work")).Style.Attrs&uv.AttrBold != 0 {
						t.Error("pending work presented as an active step")
					}
				})
			}
		}
	}
}
