package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestTimelinePersistencePolicyComesFromAcceptedReducerChanges(t *testing.T) {
	ui := tui.New()
	ui.ReplayTranscriptEvents(transcript.TurnStarted{TurnID: "turn"})
	if got := ui.PendingTranscriptPersistence(); got != "immediate" {
		t.Fatalf("turn-start policy = %q, want immediate", got)
	}
	ui.DrainTranscriptPersistence()
	if got := ui.PendingTranscriptPersistence(); got != "none" {
		t.Fatalf("drained policy = %q, want none", got)
	}

	ui.ReplayTranscriptEvents(transcript.AssistantDelta{TurnID: "turn", Delta: "partial"})
	if got := ui.PendingTranscriptPersistence(); got != "debounced" {
		t.Fatalf("stream policy = %q, want debounced", got)
	}
	ui.ReplayTranscriptEvents(transcript.ToolStarted{TurnID: "turn", ToolID: "tool", Name: "read"})
	if got := ui.PendingTranscriptPersistence(); got != "immediate" {
		t.Fatalf("boundary did not dominate stream policy: %q", got)
	}
}

func TestNoOpReducerChangeSchedulesNoPersistence(t *testing.T) {
	ui := tui.New()
	ui.ReplayTranscriptEvents(transcript.TurnStarted{TurnID: "turn"})
	ui.DrainTranscriptPersistence()
	ui.ReplayTranscriptEvents(transcript.AssistantDelta{TurnID: "turn", Delta: ""})
	if got := ui.PendingTranscriptPersistence(); got != "none" {
		t.Fatalf("no-op policy = %q, want none", got)
	}
}
