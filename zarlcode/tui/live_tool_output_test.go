package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func liveOutputFixture() *tui.UI {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", Parameters: map[string]any{"command": "printf synthetic"}})
	expandFirstTool(ui)
	return ui
}

func TestLiveBashOutputReplacesSnapshotsAndFinalResult(t *testing.T) {
	ui := liveOutputFixture()
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", Text: "FIRST_LIVE_LINE\n"})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "live output:") || strings.Count(view, "FIRST_LIVE_LINE") != 1 {
		t.Fatalf("running output missing or repeated: %s", view)
	}
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", Text: "FIRST_LIVE_LINE\nSECOND_LIVE_LINE\n"})
	view = ansi.Strip(ui.View().Content)
	if strings.Count(view, "FIRST_LIVE_LINE") != 1 || !strings.Contains(view, "SECOND_LIVE_LINE") {
		t.Fatal("cumulative preview was appended instead of replaced")
	}
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", FormattedResult: "FIRST_LIVE_LINE\nSECOND_LIVE_LINE\n[exit 0]\n"})
	view = ansi.Strip(ui.View().Content)
	if strings.Contains(view, "live output:") || strings.Count(view, "FIRST_LIVE_LINE") != 1 || !strings.Contains(view, "[exit 0]") {
		t.Fatalf("final result duplicated or lost provisional text: %s", view)
	}
	before := ui.CanonicalThread().Revision()
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", Text: "STALE_AFTER_FINISH\n"})
	if ui.CanonicalThread().Revision() != before || strings.Contains(ansi.Strip(ui.View().Content), "STALE_AFTER_FINISH") {
		t.Fatal("late output reopened a finished invocation")
	}
}

func TestLiveBashOutputRespectsCollapsedResult(t *testing.T) {
	ui := liveOutputFixture()
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: "VISIBLE_FIRST\n"})
	toolViewKey(ui, 'r')
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: "VISIBLE_FIRST\nHIDDEN_UPDATE\n"})
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "VISIBLE_FIRST") || strings.Contains(view, "HIDDEN_UPDATE") {
		t.Fatal("progress automatically expanded the result drawer")
	}
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", FormattedResult: "HIDDEN_FINAL\n[exit 0]"})
	if strings.Contains(ansi.Strip(ui.View().Content), "HIDDEN_FINAL") {
		t.Fatal("completion reopened a collapsed live result")
	}
}

func TestLiveOutputFollowsNewestRetainedRows(t *testing.T) {
	ui := liveOutputFixture()
	var output strings.Builder
	for index := range 70 {
		fmt.Fprintf(&output, "LIVE_ROW_%02d\n", index)
	}
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: output.String()})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "LIVE_ROW_69") || !strings.Contains(view, "earlier preview rows hidden") || strings.Contains(view, "LIVE_ROW_00") {
		t.Fatalf("long-running result stopped following new progress: %s", view)
	}
}

func TestLiveOutputDisplayStorageBoundaryIsBoundedAndSafe(t *testing.T) {
	ui := liveOutputFixture()
	text := "Authorization: Basic LIVE_SECRET_CANARY\n\x1b[2J\u202e\n" + strings.Repeat("界", tools.MaxToolOutputPreviewBytes)
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: text})
	var preview string
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL {
			preview = entry.Payload.ToolOutputPreview
		}
	}
	if preview == "" || len(preview) > tools.MaxToolOutputPreviewBytes || strings.Contains(preview, "LIVE_SECRET_CANARY") || strings.Contains(preview, "\x1b[2J") || strings.Contains(preview, "\u202e") {
		t.Fatal("unsafe or unbounded preview crossed canonical boundary")
	}
	if err := ui.CanonicalThread().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveOutputRestoreUsesHonestBoundedPreview(t *testing.T) {
	ui := liveOutputFixture()
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: "RECOVERED_PARTIAL_OUTPUT\n"})
	resumed := tui.New()
	recovered, changed := ui.CanonicalThread().RecoverInterrupted()
	if !changed {
		t.Fatal("active call was not marked interrupted during recovery")
	}
	resumed.RestoreCanonicalTranscript(recovered)
	resumed.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	expandFirstTool(resumed)
	view := ansi.Strip(resumed.View().Content)
	if !strings.Contains(view, "RECOVERED_PARTIAL_OUTPUT") || !strings.Contains(view, "output preview (bounded):") {
		t.Fatalf("interrupted restore lost or mislabelled partial output: %s", view)
	}
}

func TestLiveOutputGrowthPreservesSelectedSibling(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	for _, id := range []string{"first", "selected", "third"} {
		ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: id, ToolID: id, ToolName: "inspect", Parameters: map[string]any{"path": id + "_INPUT"}})
	}
	expandFirstTool(ui)
	toolViewKey(ui, tea.KeyDown)
	toolViewKey(ui, tea.KeyEnter)
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "first", ToolID: "first", Text: strings.Repeat("new output row\n", 20)})
	toolViewKey(ui, 'j')
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, `"path": "selected_INPUT"`) || strings.Contains(view, `"path": "third_INPUT"`) {
		t.Fatalf("live output growth retargeted selected sibling: %s", view)
	}
}
