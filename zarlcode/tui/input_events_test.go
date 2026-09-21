package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestInputEventsProjectOnlyCurrentRun(t *testing.T) {
	ui := newActivityUI()
	ui.Update(teasink.WaitingForInputsMsg{TaskID: "turn", Waiting: true})
	assertLiveActivity(t, ui, "waiting for agents")
	ui.Update(teasink.WaitingForInputsMsg{TaskID: "other", Waiting: false})
	assertLiveActivity(t, ui, "waiting for agents")
	ui.Update(teasink.InputsAdmittedMsg{TaskID: "turn", References: []tools.AdmissionReference{{Namespace: "spawn.completion", ID: "child"}}, Messages: []llm.Message{{Observation: llm.ObservationProvenance{Version: 1, ID: "observation"}, Content: `{"kind":"agent_completion","child_id":"child","state":"completed"}`}}})
	out := ansi.Strip(ui.View().Content)
	if strings.Contains(out, "Child child: completed result admitted") {
		t.Fatalf("orphan admission leaked into conversation:\n%s", out)
	}
	entries := ui.CanonicalThread().Entries()
	if got := entries[len(entries)-1]; got.Kind != transcript.EntryKinds.ENTRYINPUTADMISSION || got.Payload.InputAdmission.ID != "child" {
		t.Fatalf("admission missing from canonical history: %#v", got)
	}
	ui.Update(teasink.InputsAdmittedMsg{TaskID: "other", References: []tools.AdmissionReference{{Namespace: "completion", ID: "stale"}}})
	if out := ansi.Strip(ui.View().Content); strings.Contains(out, "Result completion/stale") {
		t.Fatalf("stale admission displayed:\n%s", out)
	}
}

func TestRestoredInputEventsDoNotReviveWaiting(t *testing.T) {
	r := transcript.NewReducer()
	for _, event := range []any{
		transcript.InputWaitChanged{TurnID: "old", Text: "Child-result wait started.", Waiting: true},
		transcript.InputAdmitted{TurnID: "old", Text: "Historical result admitted.", Admission: transcript.InputAdmission{Namespace: "completion", ID: "child"}},
	} {
		if _, err := r.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	ui := tui.New()
	ui.RestoreCanonicalTranscript(r.Thread())
	ui.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	out := ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "Historical result admitted.") || !strings.Contains(out, "○ idle") || strings.Contains(out, "⠋ waiting") {
		t.Fatalf("historical input projection revived activity:\n%s", out)
	}
}

func TestAgentAdmissionsStayUnderCompletedAgent(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "automatic"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			live := newActivityUI()
			for _, id := range []string{"sibling", "child"} {
				live.Update(teasink.ConversationStartedMsg{TaskID: id, Depth: 1, AgentName: id, Prompt: "review work"})
				live.Update(teasink.ConversationEndedMsg{TaskID: id, Depth: 1, Reason: runner.TerminalCompleted})
			}
			event := teasink.InputsAdmittedMsg{TaskID: "turn", References: []tools.AdmissionReference{{Namespace: "spawn.completion", ID: "child"}}}
			want := "Result spawn.completion/child admitted through explicit tool output."
			if !explicit {
				event.Messages = []llm.Message{{Observation: llm.ObservationProvenance{Version: 1, ID: "observation"}, Content: `{"kind":"agent_completion","child_id":"child","state":"completed"}`}}
				want = "Child child: completed result admitted"
			}
			live.Update(event)
			live.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: runner.TerminalCompleted})
			thread := live.CanonicalThread()
			for _, entry := range thread.Entries() {
				if entry.Kind == transcript.EntryKinds.ENTRYINPUTADMISSION && entry.TurnID != "turn" {
					t.Fatalf("presentation changed admission ownership: %#v", entry)
				}
			}
			records, err := thread.RecordsSince(0)
			if err != nil {
				t.Fatal(err)
			}
			restoredThread, err := transcript.FromRecords(thread.Revision(), records)
			if err != nil {
				t.Fatal(err)
			}
			restored := tui.New()
			restored.RestoreCanonicalTranscript(restoredThread)
			assertAdmissionUnderAgent(t, live, want)
			assertAdmissionUnderAgent(t, restored, want)
		})
	}
}

func assertAdmissionUnderAgent(t *testing.T, ui *tui.UI, want string) {
	t.Helper()
	ui.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	out := ansi.Strip(ui.View().Content)
	if strings.Contains(out, want) || !strings.Contains(out, "○ idle") {
		t.Fatalf("admission leaked into conversation or revived activity:\n%s", out)
	}
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'})
	out = ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "task: sibling") || strings.Contains(out, want) {
		t.Fatalf("admission routed to wrong agent:\n%s", out)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	out = ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "task: child") || !strings.Contains(out, "[complete]") || strings.Count(out, want) != 1 {
		t.Fatalf("completed agent detail missing admission:\n%s", out)
	}
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'})
}
