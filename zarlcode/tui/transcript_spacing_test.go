package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestCompactTranscriptSeparatesTurnsNotActivity(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	live := tui.New()
	live.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	live.Update(teasink.ConversationStartedMsg{TaskID: "compact", Prompt: "USER_LAST"})
	live.Update(teasink.ContentMsg{TaskID: "compact", Delta: "FIRST_PARAGRAPH\n\nLAST_PARAGRAPH"})
	live.Update(teasink.ToolStartedMsg{TaskID: "compact", ToolID: "call", ExecutionID: "call", ToolName: "bash"})
	live.Update(teasink.ToolCompletedMsg{TaskID: "compact", ToolID: "call", ExecutionID: "call", ToolName: "bash", FormattedResult: "done"})
	live.Update(teasink.ConversationEndedMsg{TaskID: "compact", Reason: runner.TerminalCompleted})
	live.Update(teasink.ConversationStartedMsg{TaskID: "next", Prompt: "NEXT_USER"})
	restored := tui.New()
	restored.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	restored.RestoreCanonicalTranscript(live.CanonicalThread())
	for _, ui := range []*tui.UI{live, restored} {
		canvas := surfaceCanvas(ui, 100, 40)
		user := surfaceRow(t, canvas, "USER_LAST")
		first := surfaceRow(t, canvas, "FIRST_PARAGRAPH")
		last := surfaceRow(t, canvas, "LAST_PARAGRAPH")
		tools := surfaceRow(t, canvas, "tools (1)")
		next := surfaceRow(t, canvas, "NEXT_USER")
		if first-1 != user+3 {
			t.Fatal("expected user bottom padding and one separator before the agent heading")
		}
		for _, row := range []int{user, next} {
			for x := range 99 {
				cell := canvas.CellAt(x, row+1)
				if cell.Content != " " || !sameSurface(cell.Style.Bg, canvas.CellAt(0, row).Style.Bg) {
					t.Fatal("user bottom padding must be blank and retain the user background")
				}
			}
		}
		if last != first+2 {
			t.Fatal("authored paragraph spacing changed")
		}
		if tools != last+1 {
			t.Fatal("extra blank row between response and activity")
		}
		if next-1 != tools+2 {
			t.Fatal("expected exactly one blank row before the next user heading")
		}
		if canvas.CellAt(2, first-1).Style.Attrs&uv.AttrBold == 0 {
			t.Fatal("agent heading is not emphasized")
		}
		if canvas.CellAt(2, first).Style.Attrs&uv.AttrBold != 0 {
			t.Fatal("heading emphasis leaked into body")
		}
	}
}

func TestCompactShellResultKeepsLiveAlignmentAndAuthoredIndentation(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, palette := range []theme.Theme{{}, theme.DarkDefault()} {
		tui.UseTheme(palette)
		for _, width := range []int{40, 100} {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			ui.Update(teasink.ConversationStartedMsg{TaskID: "compact", Prompt: "inspect"})
			ui.Update(teasink.ToolStartedMsg{TaskID: "compact", ToolID: "call", ExecutionID: "call", ToolName: "bash", Parameters: map[string]any{"command": "synthetic"}})
			ui.Update(teasink.ToolOutputMsg{TaskID: "compact", ToolID: "call", ExecutionID: "call", ToolName: "bash", Text: "PLAIN\n  INDENT │ pipe\né combining"})
			clickToolLabel(t, ui, "[+] tools")
			clickToolLabel(t, ui, "[+] ◌ bash")
			before := ansi.Strip(ui.View().Content)
			ui.Update(teasink.ToolCompletedMsg{TaskID: "compact", ToolID: "call", ExecutionID: "call", ToolName: "bash", FormattedResult: "PLAIN\n  INDENT │ pipe\né combining"})
			after := ansi.Strip(ui.View().Content)
			for _, text := range []string{"        PLAIN", "          INDENT │ pipe", "        é combining"} {
				if !strings.Contains(before, text) || !strings.Contains(after, text) {
					t.Fatalf("live/final output shifted or lost authored indentation at width %d: %q\n%s", width, text, after)
				}
			}
			clickToolLabel(t, ui, "result:")
			if strings.Contains(ansi.Strip(ui.View().Content), "PLAIN") {
				t.Fatal("compacted drawer no longer collapses")
			}
		}
	}
}
