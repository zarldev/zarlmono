package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestSkillsAndPlanAlignWithActivitySections(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 220, Height: 60})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "aligned", Prompt: "inspect"})
	ui.Update(teasink.ContentMsg{TaskID: "aligned", Delta: "RESPONSE"})
	ui.Update(teasink.ThinkingMsg{TaskID: "aligned", Delta: "thinking body"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "aligned", ToolID: "skill", ToolName: "skill_load", Parameters: map[string]any{"name": "go-testing"}})
	ui.Update(teasink.ToolCompletedMsg{TaskID: "aligned", ToolID: "skill", ToolName: "skill_load", Result: "loaded"})
	ui.Update(teasink.PlanUpdatedMsg{TaskID: "aligned", Plan: code.Plan{Steps: []code.PlanStep{{Text: "ALIGN_STEP", Status: code.StepStatuses.INPROGRESS}}}})
	restored := tui.New()
	restored.Update(tea.WindowSizeMsg{Width: 220, Height: 60})
	restored.RestoreCanonicalTranscript(ui.CanonicalThread())
	for _, candidate := range []*tui.UI{ui, restored} {
		for _, width := range []int{220, 60} {
			candidate.Update(tea.WindowSizeMsg{Width: width, Height: 60})
			canvas := surfaceCanvas(candidate, width, 60)
			for _, label := range []string{"[+] skills", "[+] plan updated", "[+] thinking", "[+] tools"} {
				row := surfaceRow(t, canvas, label)
				if got := strings.Index(transcriptScreenRow(canvas, row), label); got != 2 {
					t.Fatalf("%s at %d, want common inset 2", label, got)
				}
			}
			clickToolLabel(t, candidate, "[+] skills")
			clickToolLabel(t, candidate, "[+] plan updated")
			canvas = surfaceCanvas(candidate, width, 60)
			for _, label := range []string{"[-] skills", "[-] plan updated"} {
				row := surfaceRow(t, canvas, label)
				if strings.Index(transcriptScreenRow(canvas, row), label) != 2 {
					t.Fatal("expanded disclosure changed alignment")
				}
			}
			row := surfaceRow(t, canvas, "• go-testing")
			if strings.Index(transcriptScreenRow(canvas, row), "• go-testing") != 4 {
				t.Fatal("skill body must retain one child indent")
			}
			row = surfaceRow(t, canvas, "1. ")
			if strings.Index(transcriptScreenRow(canvas, row), "1. ") != 4 {
				t.Fatal("plan steps must retain one child indent")
			}
			clickToolLabel(t, candidate, "[-] skills")
			clickToolLabel(t, candidate, "[-] plan updated")
		}
	}
}
