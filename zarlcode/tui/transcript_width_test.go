package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func transcriptScreenRow(canvas uv.ScreenBuffer, row int) string {
	var line strings.Builder
	for x := range canvas.Bounds().Dx() {
		line.WriteString(canvas.CellAt(x, row).Content)
	}
	return line.String()
}

func TestTranscriptTextUsesAvailablePaneWidthAcrossResize(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 220, Height: 60})
	// Each marker falls beyond the former 110-column cap but within the wide
	// pane. Preserve spaces so this also exercises ordinary word wrapping.
	ui.AddTranscriptMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "USER_START " + strings.Repeat("word ", 24) + "USER_END"},
		{Role: llm.RoleAssistant, Content: "AGENT_START " + strings.Repeat("word ", 24) + "AGENT_END"},
	})
	for _, width := range []int{220, 60, 280, 100, 220} {
		ui.Update(tea.WindowSizeMsg{Width: width, Height: 60})
		canvas := surfaceCanvas(ui, width, 60)
		for _, role := range []string{"USER", "AGENT"} {
			start := surfaceRow(t, canvas, role+"_START")
			end := surfaceRow(t, canvas, role+"_END")
			if width >= 220 {
				if start != end || strings.Index(transcriptScreenRow(canvas, end), role+"_END") <= 110 {
					t.Fatalf("%s still wraps at the old cap at width %d, rows %d/%d:\n%s", role, width, start, end, canvas.String())
				}
			} else if end <= start {
				t.Fatalf("%s did not wrap to narrow pane at width %d", role, width)
			}
		}
		paneWidth := width
		if width >= 160 {
			paneWidth -= 56
		}
		for row := 2; row < 56; row++ {
			if canvas.CellAt(paneWidth-2, row).Content != " " {
				t.Fatalf("text spilled into right padding at width %d row %d", width, row)
			}
		}
		// Browse redraws must use the same width as follow-mode rendering.
		ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		canvas = surfaceCanvas(ui, width, 60)
		if width >= 220 && surfaceRow(t, canvas, "AGENT_START") != surfaceRow(t, canvas, "AGENT_END") {
			t.Fatal("browsing reintroduced a narrower text cap")
		}
	}
}

func TestWideToolDrawerUsesSameTextLane(t *testing.T) {
	ui := drawerFixture()
	ui.Update(tea.WindowSizeMsg{Width: 220, Height: 60})
	_ = ui.View()
	clickToolLabel(t, ui, "arguments:")
	clickToolLabel(t, ui, "arguments:")
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: "RESULT_START " + strings.Repeat("word ", 24) + "RESULT_END"})
	canvas := surfaceCanvas(ui, 220, 60)
	_ = surfaceRow(t, canvas, "INPUT_MARKER")
	start, end := surfaceRow(t, canvas, "RESULT_START"), surfaceRow(t, canvas, "RESULT_END")
	if start != end || strings.Index(transcriptScreenRow(canvas, end), "RESULT_END") <= 110 {
		t.Fatal("tool result still uses a fixed narrow wrap width")
	}
	clickToolLabel(t, ui, "[-] tools")
	if strings.Contains(ui.View().Content, "INPUT_MARKER") {
		t.Fatal("wide group collapse leaked tool details")
	}
}
