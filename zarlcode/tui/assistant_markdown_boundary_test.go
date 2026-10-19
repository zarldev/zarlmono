package tui_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestAssistantMarkdownSeparatesCompletedResponsesWithoutChangingText(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, tc := range []struct{ name, first, second string }{
		{name: "sentence seam", first: "The database will stay read-only.", second: "All four live checks passed."},
		{name: "bold seam", first: "**Nothing was published or deployed.**", second: "**Verified offline:**\n\n- Current smoke harness compiles."},
		{name: "unclosed fence", first: "```text\nearlier fragment", second: "**Verified offline:**\n\n- Current smoke harness compiles."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
			ui.Update(teasink.ConversationStartedMsg{TaskID: "markdown-turn", Prompt: "inspect"})
			ui.Update(teasink.ContentMsg{TaskID: "markdown-turn", Delta: tc.first})
			_ = ui.View() // Seed the live renderer before the response boundary.
			ui.Update(teasink.IterationCompletedMsg{TaskID: "markdown-turn", Iter: 0})
			ui.Update(teasink.ContentMsg{TaskID: "markdown-turn", Delta: tc.second})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "markdown-turn", Reason: runner.TerminalCompleted})
			view := ansi.Strip(ui.View().Content)
			if strings.Contains(view, "**Verified offline") || strings.Contains(view, "**Nothing was published") {
				t.Fatalf("formatting markers leaked at a response seam:\n%s", view)
			}
			if tc.name == "sentence seam" && strings.Contains(view, "read-only.All four") {
				t.Fatalf("independent responses ran together:\n%s", view)
			}
			for _, entry := range ui.CanonicalThread().Entries() {
				if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE && entry.Payload.Text != tc.first+tc.second {
					t.Fatal("presentation fix changed canonical provider text")
				}
			}
			if !strings.Contains(view, " agent") {
				t.Fatal("assistant heading disappeared")
			}
		})
	}
}

func TestAssistantMarkdownOutputItemsRemainSeparateWithinOneIteration(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "items", Prompt: "inspect"})
	for _, delta := range []string{"**Nothing was ", "published or deployed.**"} {
		ui.Update(teasink.ContentMsg{TaskID: "items", Delta: delta, ContentOutputIndex: llm.OutputPosition(0)})
	}
	ui.Update(teasink.ContentMsg{TaskID: "items", Delta: "**Verified offline:**", ContentOutputIndex: llm.OutputPosition(2)})
	ui.Update(teasink.IterationCompletedMsg{TaskID: "items", Iter: 0})
	// Output item indices are response-local: item zero is valid again.
	ui.Update(teasink.ContentMsg{TaskID: "items", Delta: "Next response.", ContentOutputIndex: llm.OutputPosition(0)})
	ui.Update(teasink.ConversationEndedMsg{TaskID: "items", Reason: runner.TerminalCompleted})
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "**Verified") || strings.Contains(view, "**Nothing") || strings.Contains(view, "offline:Next") {
		t.Fatalf("output item seam not rendered independently:\n%s", view)
	}
	var assistant []transcript.Payload
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE {
			assistant = append(assistant, entry.Payload)
		}
	}
	if len(assistant) != 1 || !reflect.DeepEqual(assistant[0].TextBreaks, []int{len("**Nothing was published or deployed.**"), len("**Nothing was published or deployed.****Verified offline:**")}) {
		t.Fatalf("chunk boundaries changed assistant identity or split same-item text: %+v", assistant)
	}
}

func TestAssistantMarkdownBoundariesSurviveSaveResume(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetSessionIdentity("markdown-boundaries", "Markdown", false, time.Now())
	f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	f.ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "inspect"})
	f.ui.Update(teasink.ContentMsg{TaskID: "turn", Delta: "**Nothing was published or deployed.**"})
	f.ui.Update(teasink.IterationCompletedMsg{TaskID: "turn", Iter: 0})
	f.ui.Update(teasink.ContentMsg{TaskID: "turn", Delta: "**Verified offline:**"})
	f.ui.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: runner.TerminalCompleted})
	f.live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "inspect"}, {Role: llm.RoleAssistant, Content: "**Nothing was published or deployed.****Verified offline:**"}})
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	resumed := tui.New()
	resumed.SetLiveRunner(f.live)
	resumed.SetSettings(settings)
	if err := resumed.ResumeSavedSession(t.Context(), "markdown-boundaries"); err != nil {
		t.Fatal(err)
	}
	resumed.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	if !reflect.DeepEqual(resumed.CanonicalThread().Entries(), f.ui.CanonicalThread().Entries()) {
		t.Fatal("resume changed canonical text or boundary metadata")
	}
	for _, ui := range []*tui.UI{f.ui, resumed} {
		view := ansi.Strip(ui.View().Content)
		if strings.Contains(view, "**Verified") || strings.Contains(view, "**Nothing") {
			t.Fatalf("live/resumed Markdown seam remains broken:\n%s", view)
		}
	}
}
