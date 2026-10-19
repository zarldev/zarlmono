package tui_test

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPlanReviewNavigationAndLiveUpdates(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	steps := []code.PlanStep{
		{Text: "FinishedAPI", Status: code.StepStatuses.COMPLETED},
		{Text: "CurrentAPI", Status: code.StepStatuses.INPROGRESS},
		{Text: "AlsoFinished", Status: code.StepStatuses.COMPLETED},
		{Text: "NextAPI", Status: code.StepStatuses.PENDING},
	}
	original := slices.Clone(steps)
	ui.SetPlan(code.Plan{Steps: steps, Explanation: "plan-level rationale"})
	ui.Update(tea.PasteMsg{Content: "retained draft"})
	before := ui.View().Content
	ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	assertDetail := func(want string) {
		t.Helper()
		text := ansi.Strip(ui.View().Content)
		if !strings.Contains(text, want) {
			t.Fatalf("missing selected detail %q:\n%s", want, text)
		}
	}
	assertDetail("step 2 of 4 · in progress")
	assertDetail("2 done · 1 in progress · 1 pending")
	assertDetail("latest plan update")
	assertDetail("plan-level rationale")
	for _, tc := range []struct {
		key  rune
		want string
	}{
		{'n', "step 4 of 4 · pending"},
		{'n', "step 2 of 4 · in progress"},
		{'p', "step 4 of 4 · pending"},
		{tea.KeyHome, "step 1 of 4 · completed"},
		{tea.KeyDown, "step 2 of 4 · in progress"},
		{tea.KeyUp, "step 1 of 4 · completed"},
		{tea.KeyEnd, "step 4 of 4 · pending"},
		{'a', "step 2 of 4 · in progress"},
	} {
		ui.Update(tea.KeyPressMsg{Code: tc.key})
		assertDetail(tc.want)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if after := ui.View().Content; after != before || !slices.Equal(steps, original) {
		t.Fatal("plan review changed underlying sidebar, transcript, draft or plan")
	}
	ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	steps[1].Status = code.StepStatuses.COMPLETED
	steps[3].Status = code.StepStatuses.INPROGRESS
	ui.SetPlan(code.Plan{Steps: steps, Explanation: "updated rationale"})
	assertDetail("step 2 of 4 · completed") // Do not steal focus on progress updates.
	assertDetail("updated rationale")
	ui.Update(tea.KeyPressMsg{Code: 'a'})
	assertDetail("step 4 of 4 · in progress")
	steps[3].Status = code.StepStatuses.COMPLETED
	ui.SetPlan(code.Plan{Steps: steps})
	ui.Update(tea.KeyPressMsg{Code: 'n'})
	assertDetail("step 4 of 4 · completed")
	ui.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "ReplacementAPI", Status: code.StepStatuses.PENDING}}})
	assertDetail("step 1 of 1 · pending")
	assertDetail("ReplacementAPI")
	ui.SetPlan(code.Plan{})
	ui.Update(tea.KeyPressMsg{Code: 'p'})
	ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	assertDetail("no structured plan yet")
}

func TestPlanReviewWheelScrollsDetailAndKeepsOutline(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	ui.SetPlan(code.Plan{Steps: []code.PlanStep{
		{Text: "CurrentAPI " + strings.Repeat("long detail 日本語 ", 80) + "FINAL_DETAIL", Status: code.StepStatuses.INPROGRESS},
		{Text: "NextAPI", Status: code.StepStatuses.PENDING},
	}})
	ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	initial := surfaceCanvas(ui, 120, 18)
	outline := screenRegion(initial, 2, 5, 46, 17)
	for range 80 {
		ui.Update(tea.MouseWheelMsg{X: 70, Y: 8, Button: tea.MouseWheelDown})
	}
	bottom := surfaceCanvas(ui, 120, 18)
	if !strings.Contains(bottom.String(), "FINAL_DETAIL") || initial.String() == bottom.String() {
		t.Fatal("wheel did not reach full selected description")
	}
	if got := screenRegion(bottom, 2, 5, 46, 17); got != outline {
		t.Fatal("detail scrolling moved the outline")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if text := ansi.Strip(ui.View().Content); !strings.Contains(text, "step 2 of 2 · pending") || !strings.Contains(text, "NextAPI") {
		t.Fatalf("selecting next step did not reset detail scroll:\n%s", text)
	}
}
