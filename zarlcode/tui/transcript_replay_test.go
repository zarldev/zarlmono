package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestLiveTranscriptReplayMatchesRestoredProjection(t *testing.T) {
	events := []any{
		transcript.UserSubmitted{Text: "inspect shutdown"},
		transcript.TurnStarted{TurnID: "turn"},
		transcript.ReasoningDelta{TurnID: "turn", Delta: "checking ownership"},
		transcript.AssistantDelta{TurnID: "turn", Delta: "use a bounded shutdown"},
		transcript.ToolStarted{TurnID: "turn", ToolID: "tool", Name: "read", Argument: "main.go"},
		transcript.ToolFinished{ToolID: "tool", Effect: "read 20 lines", DurationMS: 12},
		transcript.TurnFinished{TurnID: "turn"},
	}
	live := tui.New()
	live.ReplayTranscriptEvents(events...)

	reducer := transcript.NewReducer()
	for _, event := range events {
		if _, err := reducer.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	restored := tui.New()
	restored.RestoreCanonicalTranscript(reducer.Thread())

	var liveModel tea.Model = live
	var restoredModel tea.Model = restored
	liveModel, _ = liveModel.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	restoredModel, _ = restoredModel.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	liveView := normalizeReplayView(ansi.Strip(liveModel.View().Content))
	restoredView := normalizeReplayView(ansi.Strip(restoredModel.View().Content))
	if liveView != restoredView {
		t.Fatalf("live and restored projections differ:\n--- live ---\n%s\n--- restored ---\n%s", liveView, restoredView)
	}
}

func normalizeReplayView(view string) string {
	lines := strings.Split(view, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.Contains(line, "zarlcode") || strings.Contains(line, "tokens") || strings.Contains(line, "ctrl+") {
			continue
		}
		kept = append(kept, strings.TrimRight(line, " "))
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
