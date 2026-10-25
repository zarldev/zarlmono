package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestMalformedToolInputsCannotExposeUnredactedCredentials(t *testing.T) {
	for _, raw := range []string{
		`{"headers":{"Authorization":"Basic MALFORMED_CANARY"},}`,
		`{"credentials":{"value":"MALFORMED_CANARY"}},`,
		`{"password":"MALFORMED_CANARY`,
	} {
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
		ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "inspect", RawArguments: raw})
		expandFirstTool(ui)
		view := ansi.Strip(ui.View().Content)
		if strings.Contains(view, "MALFORMED_CANARY") {
			t.Fatal("malformed raw input leaked into running view")
		}
		for _, entry := range ui.CanonicalThread().Entries() {
			if strings.Contains(entry.Payload.ToolInput, "MALFORMED_CANARY") {
				t.Fatal("malformed raw input leaked into durable snapshot")
			}
		}
		exported := transcript.Markdown(transcript.MarkdownMetadata{Title: "safe"}, ui.CanonicalThread())
		if strings.Contains(exported, "MALFORMED_CANARY") {
			t.Fatal("malformed input leaked into export")
		}
	}
}

func TestRepairedToolArgumentsAreSanitizedInsteadOfMalformedSource(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "inspect", RawArguments: `{"credentials":{"token":"raw-canary"}},`, Parameters: map[string]any{"operation": "repaired-operation", "credentials": map[string]any{"token": "repaired-canary"}}})
	expandFirstTool(ui)
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "repaired-operation") || strings.Contains(view, "raw-canary") || strings.Contains(view, "repaired-canary") {
		t.Fatalf("repaired call was missing or exposed credential content:\n%s", view)
	}
}

func TestExpandedRunningToolWrapsLongArgumentLines(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 100})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": strings.Repeat("long-command-part ", 35) + "VISIBLE_ARGUMENT_TAIL"}})
	expandFirstTool(ui)
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "VISIBLE_ARGUMENT_TAIL") {
		t.Fatalf("argument tail clipped at viewport width:\n%s", view)
	}
}
