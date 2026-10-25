package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func activityCall(ui *tui.UI, index int, outcome string) {
	id := fmt.Sprintf("call-%02d", index)
	ui.Update(teasink.ToolStartedMsg{TaskID: "activity", ExecutionID: id, ToolID: id, ToolName: "read", Parameters: map[string]any{"path": fmt.Sprintf("TARGET_%02d", index)}})
	switch outcome {
	case "ok":
		ui.Update(teasink.ToolCompletedMsg{TaskID: "activity", ExecutionID: id, ToolID: id, ToolName: "read", FormattedResult: fmt.Sprintf("EVIDENCE_%02d", index)})
	case "failed":
		ui.Update(teasink.ToolFailedMsg{TaskID: "activity", ExecutionID: id, ToolID: id, ToolName: "read", Error: "synthetic failure"})
	}
}

func activityFixture(t testing.TB) *tui.UI {
	t.Helper()
	tui.UseTheme(theme.DarkDefault())
	t.Cleanup(func() { tui.UseTheme(theme.Theme{}) })
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "activity", Prompt: "Inspect without hiding important activity."})
	ui.Update(teasink.ContentMsg{TaskID: "activity", Delta: "ANSWER"})
	return ui
}

func TestActivityTrailToolsDisclosureIsAllOrNothing(t *testing.T) {
	ui := activityFixture(t)
	activityCall(ui, 0, "failed")
	activityCall(ui, 1, "running")
	for i := 2; i < 9; i++ {
		activityCall(ui, i, "ok")
	}
	restored := tui.New()
	restored.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	restored.RestoreCanonicalTranscript(ui.CanonicalThread())
	for _, candidate := range []*tui.UI{ui, restored} {
		view := ansi.Strip(candidate.View().Content)
		if !strings.Contains(view, "[+] tools (9)") || !strings.Contains(view, "1 failed") || strings.Contains(view, "TARGET_") {
			t.Fatalf("collapsed tools must be summary-only:\n%s", view)
		}
		before := candidate.CanonicalThread().Revision()
		clickToolLabel(t, candidate, "[+] tools")
		view = ansi.Strip(candidate.View().Content)
		for i := range 9 {
			if !strings.Contains(view, fmt.Sprintf("TARGET_%02d", i)) {
				t.Fatalf("expanded group omitted call %d", i)
			}
		}
		if strings.Contains(view, "more actions") || strings.Contains(view, "show recent") || strings.Contains(view, "EVIDENCE_") {
			t.Fatal("group expansion must show every call, not a subset or its evidence")
		}
		clickToolLabel(t, candidate, "TARGET_02")
		view = ansi.Strip(candidate.View().Content)
		if !strings.Contains(view, "path: TARGET_02") {
			t.Fatal("call disclosure lost arguments")
		}
		if candidate == ui && (!strings.Contains(view, "EVIDENCE_02") || strings.Contains(view, "EVIDENCE_03")) {
			t.Fatal("call disclosure opened the wrong evidence")
		}
		clickToolLabel(t, candidate, "[-] tools")
		view = ansi.Strip(candidate.View().Content)
		if strings.Contains(view, "TARGET_") || strings.Contains(view, "EVIDENCE_") {
			t.Fatal("collapsed group leaked an expanded child")
		}
		clickToolLabel(t, candidate, "[+] tools")
		if !strings.Contains(ansi.Strip(candidate.View().Content), "path: TARGET_02") {
			t.Fatal("group collapse discarded child disclosure state")
		}
		if candidate.CanonicalThread().Revision() != before {
			t.Fatal("disclosure changed execution record")
		}
	}
}

func TestActivityTrailKeepsManualExpansionAtCompletion(t *testing.T) {
	ui := activityFixture(t)
	for i := range 9 {
		activityCall(ui, i, "ok")
	}
	clickToolLabel(t, ui, "[+] tools")
	clickToolLabel(t, ui, "TARGET_00")
	ui.Update(teasink.ConversationEndedMsg{TaskID: "activity", Reason: runner.TerminalCompleted})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "[-] tools") || !strings.Contains(view, "EVIDENCE_00") || !strings.Contains(view, "TARGET_08") {
		t.Fatal("completion reset manual inspection")
	}
}

func TestActivityTrailRestoredInterruptionRemainsInSummary(t *testing.T) {
	ui := activityFixture(t)
	activityCall(ui, 0, "running")
	for i := 1; i < 9; i++ {
		activityCall(ui, i, "ok")
	}
	recovered, changed := ui.CanonicalThread().RecoverInterrupted()
	if !changed {
		t.Fatal("fixture has no recoverable call")
	}
	restored := tui.New()
	restored.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	restored.RestoreCanonicalTranscript(recovered)
	view := ansi.Strip(restored.View().Content)
	if !strings.Contains(view, "1 interrupted") || strings.Contains(view, "TARGET_") {
		t.Fatal("collapsed group must summarize, not expose, interrupted calls")
	}
	clickToolLabel(t, restored, "[+] tools")
	if !strings.Contains(ansi.Strip(restored.View().Content), "TARGET_00") {
		t.Fatal("interrupted call missing from full list")
	}
}

func TestActivityTrailNestedFailureRemainsInspectable(t *testing.T) {
	ui := activityFixture(t)
	ui.Update(teasink.ToolStartedMsg{TaskID: "activity", ExecutionID: "parent", ToolID: "parent", ToolName: "program"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "activity", ExecutionID: "child", ToolID: "child", ParentToolID: "parent", ParentExecutionID: "parent", ToolName: "inspect"})
	ui.Update(teasink.ToolFailedMsg{TaskID: "activity", ExecutionID: "child", ToolID: "child", ParentToolID: "parent", ParentExecutionID: "parent", ToolName: "inspect", Error: "synthetic failure"})
	ui.Update(teasink.ToolCompletedMsg{TaskID: "activity", ExecutionID: "parent", ToolID: "parent", ToolName: "program", FormattedResult: "completed"})
	clickToolLabel(t, ui, "[+] tools")
	if !strings.Contains(ansi.Strip(ui.View().Content), "1 failed") {
		t.Fatal("program header lost failed-child summary")
	}
	clickToolLabel(t, ui, "program")
	if !strings.Contains(ansi.Strip(ui.View().Content), "✗ inspect") {
		t.Fatal("failed child unreachable")
	}
}

// BenchmarkActivityTrailView measures warmed collapsed-group redraws. Hidden
// calls must not add history-wide work to ordinary frame rendering.
func BenchmarkActivityTrailView(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ui := activityFixture(b)
			for i := range count {
				activityCall(ui, i, "ok")
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
