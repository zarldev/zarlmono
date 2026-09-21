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
)

func TestModeChangesAreExpandableTurnActivity(t *testing.T) {
	for _, reasoning := range []string{"before", "after", "none"} {
		t.Run(reasoning, func(t *testing.T) {
			live := tui.New()
			live.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			live.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "test modes"})
			live.Update(teasink.ContentMsg{TaskID: "turn", Delta: "The answer stays conversational."})
			if reasoning == "before" {
				live.Update(teasink.ThinkingMsg{TaskID: "turn", Delta: "consider the boundaries"})
			}
			live.Update(teasink.ModeChangedMsg{TaskID: "turn", Plan: true, Reason: "investigate before implementation", Generation: 1})
			if reasoning == "after" {
				live.Update(teasink.ThinkingMsg{TaskID: "turn", Delta: "consider the boundaries"})
			}
			live.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"})
			live.Update(teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"})
			live.Update(teasink.ModeChangedMsg{TaskID: "turn", Reason: "implement the verified approach", Generation: 2})
			live.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: runner.TerminalCompleted})

			thread := live.CanonicalThread()
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

			for _, tc := range []struct {
				name string
				ui   *tui.UI
			}{{"live", live}, {"restored", restored}} {
				t.Run(tc.name, func(t *testing.T) {
					tc.ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
					out := ansi.Strip(tc.ui.View().Content)
					for _, want := range []string{"The answer stays conversational.", "│ [+] thinking", "│ [+] tools (1)"} {
						if !strings.Contains(out, want) {
							t.Fatalf("missing %q:\n%s", want, out)
						}
					}
					if strings.Count(out, "[+] thinking") != 1 {
						t.Fatalf("expected one thinking disclosure:\n%s", out)
					}
					for _, hidden := range []string{"Plan:", "Build:", "] plan mode", "] build mode", "investigate before implementation", "implement the verified approach"} {
						if strings.Contains(out, hidden) {
							t.Fatalf("collapsed thinking leaked %q:\n%s", hidden, out)
						}
					}
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // tools -> thinking
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					out = ansi.Strip(tc.ui.View().Content)
					for _, want := range []string{"[-] thinking", "│   [+] plan mode", "│   [+] build mode"} {
						if !strings.Contains(out, want) {
							t.Fatalf("missing thinking child %q:\n%s", want, out)
						}
					}
					if reasoning != "none" && !strings.Contains(out, "consider the boundaries") {
						t.Fatalf("thinking text missing:\n%s", out)
					}
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // thinking -> plan
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					out = ansi.Strip(tc.ui.View().Content)
					if !strings.Contains(out, "[-] plan mode") || !strings.Contains(out, "investigate before implementation") || strings.Contains(out, "implement the verified approach") {
						t.Fatalf("plan child disclosure:\n%s", out)
					}
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // plan -> build
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					out = ansi.Strip(tc.ui.View().Content)
					if !strings.Contains(out, "[-] build mode") || !strings.Contains(out, "implement the verified approach") {
						t.Fatalf("expanded mode missing detail:\n%s", out)
					}
					tc.ui.Update(tea.WindowSizeMsg{Width: 54, Height: 40})
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyUp})
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // build -> plan -> thinking
					tc.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					out = ansi.Strip(tc.ui.View().Content)
					for _, hidden := range []string{"] plan mode", "] build mode", "investigate before", "implement the verified"} {
						if strings.Contains(out, hidden) {
							t.Fatalf("thinking did not hide %q:\n%s", hidden, out)
						}
					}
				})
			}
		})
	}
}

func TestLegacyModeNoticesRestoreAsActivity(t *testing.T) {
	ui := tui.New()
	reducer := transcript.NewReducer()
	for _, event := range []any{
		transcript.TurnStarted{TurnID: "turn"},
		transcript.AssistantDelta{TurnID: "turn", Delta: "answer"},
		transcript.NoticeAdded{Text: "ordinary notice"},
		transcript.NoticeAdded{Text: "\x1b[90mPlan: historical reason\x1b[0m"},
		transcript.TurnFinished{TurnID: "turn"},
	} {
		if _, err := reducer.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	ui.RestoreCanonicalTranscript(reducer.Thread())
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	out := ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "ordinary notice") || !strings.Contains(out, "│ [+] thinking") || strings.Contains(out, "plan mode") || strings.Contains(out, "historical reason") {
		t.Fatalf("legacy notice projection:\n%s", out)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	out = ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "│   [+] plan mode") || strings.Contains(out, "historical reason") {
		t.Fatalf("legacy thinking child:\n%s", out)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if out := ansi.Strip(ui.View().Content); !strings.Contains(out, "historical reason") {
		t.Fatalf("legacy mode reason missing:\n%s", out)
	}
}
