package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestToolDrawerHeadersUseLowercaseThemeColourWithoutKeyHints(t *testing.T) {
	palette := theme.DarkDefault()
	palette.Subtle = theme.Color("#12ab34")
	tui.UseTheme(palette)
	defer tui.UseTheme(theme.Theme{})
	for _, phase := range []string{"running", "complete", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": "printf safe"}})
			ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", Text: "safe progress\n"})
			resultHeader := "live output:"
			switch phase {
			case "complete":
				ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "bash", FormattedResult: "safe final\n[exit 0]"})
				resultHeader = "result:"
			case "interrupted":
				recovered, _ := ui.CanonicalThread().RecoverInterrupted()
				ui.RestoreCanonicalTranscript(recovered)
				resultHeader = "output preview (bounded):"
			}
			expandFirstTool(ui)
			raw := ui.View().Content
			plain := ansi.Strip(raw)
			for _, hint := range []string{"[a]", "[r]", "[j]", "[j:"} {
				if strings.Contains(plain, hint) {
					t.Errorf("inline key hint %q still visible", hint)
				}
			}
			for _, header := range []string{"view: readable", "arguments:", resultHeader} {
				found := false
				for _, line := range strings.Split(raw, "\n") {
					if strings.Contains(ansi.Strip(line), header) {
						found = true
						if !strings.Contains(line, palette.Subtle.FG()) {
							t.Errorf("header %q lacks configured secondary text colour", header)
						}
					}
				}
				if !found {
					t.Errorf("missing lowercase header %q: %s", header, plain)
				}
			}
			toolViewKey(ui, 'j')
			if !strings.Contains(ansi.Strip(ui.View().Content), "view: json") {
				t.Fatal("hidden shortcut stopped switching representation")
			}
		})
	}
}
