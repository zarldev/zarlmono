package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestActivityLayoutTracksThemeAndSectionHierarchy(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "theme", Prompt: "REQUEST"})
	ui.Update(teasink.ContentMsg{TaskID: "theme", Delta: "RESPONSE"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "theme", ExecutionID: "call", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": "printf example"}})
	ui.Update(teasink.ToolCompletedMsg{TaskID: "theme", ExecutionID: "call", ToolID: "call", ToolName: "bash", FormattedResult: "RESULT_BODY"})
	clickToolLabel(t, ui, "[+] tools")
	clickToolLabel(t, ui, "[+] ✓ bash")
	// Reuse the same rendered items across theme switches to exercise cache
	// invalidation, rather than testing only freshly constructed fixtures.
	for _, name := range []string{"midnight", "parchment", "dracula"} {
		palette, ok := theme.ByName(name)
		if !ok {
			t.Fatalf("missing theme %s", name)
		}
		tui.UseTheme(palette)
		canvas := surfaceCanvas(ui, 100, 40)
		user, agent := palette.ConversationBackgrounds()
		for _, label := range []string{"RESPONSE", "tools (1)", "✓ bash", "arguments:", "result:", "RESULT_BODY"} {
			row := surfaceRow(t, canvas, label)
			if !sameSurface(canvas.CellAt(0, row).Style.Bg, lg.Color(string(agent))) {
				t.Fatalf("%s: %s retained another theme's background", name, label)
			}
		}
		row := surfaceRow(t, canvas, "REQUEST")
		if !sameSurface(canvas.CellAt(0, row).Style.Bg, lg.Color(string(user))) {
			t.Fatalf("%s user background did not update", name)
		}
		for _, tc := range []struct {
			label string
			x     int
		}{
			{"RESPONSE", 2}, {"[-] tools (1)", 2}, {"[-] ✓ bash", 4}, {"view: readable", 6}, {"[-] arguments:", 6}, {"[-] result:", 6},
			// Shell output shares the drawer's value inset, without code padding.
			{"RESULT_BODY", 8},
		} {
			row := surfaceRow(t, canvas, tc.label)
			var text strings.Builder
			for x := range 100 {
				text.WriteString(canvas.CellAt(x, row).Content)
			}
			if got := strings.Index(text.String(), tc.label); got != tc.x {
				t.Fatalf("%s starts at %d, want %d", tc.label, got, tc.x)
			}
		}
		row = surfaceRow(t, canvas, "✓ bash")
		if !sameSurface(canvas.CellAt(10, row).Style.Fg, lg.Color(string(palette.Tool))) {
			t.Fatalf("%s tool name does not use its semantic accent", name)
		}
		// Controls still target the same owner after the shallower layout.
		clickToolLabel(t, ui, "result:")
		if strings.Contains(ansi.Strip(ui.View().Content), "RESULT_BODY") {
			t.Fatal("result drawer did not collapse")
		}
		clickToolLabel(t, ui, "result:")
	}
}
