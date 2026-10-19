package tui_test

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// BenchmarkSidebarPopulatedView measures the entire screen with accumulated
// workspace mutations and a long plan, both with and without the summary pane.
func BenchmarkSidebarPopulatedView(b *testing.B) {
	for _, size := range []int{100, 10000} {
		for _, hidden := range []bool{false, true} {
			b.Run(fmt.Sprintf("files=%d/hidden=%t", size, hidden), func(b *testing.B) {
				ui := sidebarFixture(160, 48)
				ui.SetWorkspace("/synthetic/workspace", "synthetic-model")
				for i := range size {
					ui.Update(teasink.DiffMsg{TaskID: "summary", Path: fmt.Sprintf("file-%d.go", i), Diff: "@@\n-old\n+new"})
				}
				steps := make([]code.PlanStep, 1000)
				for i := range steps {
					steps[i] = code.PlanStep{Text: "Synthetic completed step", Status: code.StepStatuses.COMPLETED}
				}
				steps[500] = code.PlanStep{Text: "Current refinement", Status: code.StepStatuses.INPROGRESS}
				ui.SetPlan(code.Plan{Steps: steps})
				if hidden {
					ui.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
				}
				_ = ui.View()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					_ = ui.View()
				}
			})
		}
	}
}
