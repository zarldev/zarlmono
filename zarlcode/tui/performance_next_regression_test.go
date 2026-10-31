package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestReusedCanvasMatchesFreshScreen(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := liveOutputFixture()
	for _, width := range []int{140, 140, 70, 180, 140} {
		ui.Update(tea.WindowSizeMsg{Width: width, Height: 100})
		for _, text := range []string{strings.Repeat("界 é wide output\n", 60), "short", ""} {
			ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: text})
			got := ui.View().Content
			fresh := uv.NewScreenBuffer(width, 100)
			ui.Draw(fresh, fresh.Bounds())
			want := strings.ReplaceAll(fresh.Render(), "\r\n", "\n")
			if got != want {
				t.Fatalf("reused frame differs at width %d after %d bytes", width, len(text))
			}
			retained := strings.Clone(got)
			ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: "different next frame"})
			_ = ui.View()
			if retained != got {
				t.Fatal("later frame changed retained View content")
			}
		}
	}
}

func TestCompletedJSONPresentationInvalidatesOnReplacement(t *testing.T) {
	ui := drawerFixture()
	for _, value := range []string{"FIRST_RESULT", "REPLACED_RESULT"} {
		ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: `{"result":"` + value + `","token":"CANARY"}`})
		for range 2 {
			toolViewKey(ui, 'j')
			view := ansi.Strip(ui.View().Content)
			if !strings.Contains(view, value) || strings.Contains(view, "CANARY") {
				t.Fatal("stale or unsafe cached result presentation")
			}
		}
	}
}

func TestLargeCompletedResultsUseBoundedSafePreview(t *testing.T) {
	for _, tool := range []string{"bash", "read", "web_fetch"} {
		t.Run(tool, func(t *testing.T) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: tool})
			text := "FIRST_RESULT_ROW\npassword=CANARY\n" + strings.Repeat("synthetic result line\n", 8000) + "FULL_RESULT_TAIL"
			ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: tool, FormattedResult: text})
			expandFirstTool(ui)
			view := ansi.Strip(ui.View().Content)
			if !strings.Contains(view, "FIRST_RESULT_ROW") || !strings.Contains(view, "plain preview; full result retained") || strings.Contains(view, "CANARY") || strings.Contains(view, "FULL_RESULT_TAIL") {
				t.Fatal("large preview lost its bounds, safety or disclosure")
			}
		})
	}
}

func TestLargeJSONResultRemainsReadableAndRedacted(t *testing.T) {
	ui := drawerFixture()
	text := `{"a":"FIRST_RESULT","password":"CANARY","rows":[` + strings.Repeat(`{"value":"synthetic"},`, 8000) + `{"value":"TAIL"}]}`
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: text})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "FIRST_RESULT") || !strings.Contains(view, "additional result content not rendered") || strings.Contains(view, "CANARY") || strings.Contains(view, "TAIL") {
		t.Fatal("large JSON preview was unbounded or unsafe")
	}
	toolViewKey(ui, 'j')
	view = ansi.Strip(ui.View().Content)
	if strings.Contains(view, "CANARY") || !strings.Contains(view, "FIRST_RESULT") {
		t.Fatal("JSON toggle bypassed redaction or lost result")
	}
}

func TestLargeJSONShapedShellAndCodeResultsStayLiteral(t *testing.T) {
	for _, tool := range []string{"bash", "read"} {
		t.Run(tool, func(t *testing.T) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: tool})
			text := `{"x":1,"x":2,"padding":"` + strings.Repeat("literal ", 10000) + `"}`
			ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: tool, FormattedResult: text})
			expandFirstTool(ui)
			view := ansi.Strip(ui.View().Content)
			if !strings.Contains(view, `{"x":1,"x":2,`) || !strings.Contains(view, "plain preview; full result retained") {
				t.Fatal("JSON-shaped literal output was reinterpreted or silently truncated")
			}
		})
	}
}

func TestLargeJSONScalarUsesBoundedPreview(t *testing.T) {
	ui := drawerFixture()
	text := `{"value":"FIRST_SCALAR ` + strings.Repeat("large scalar ", 20000) + `TAIL_SCALAR"}`
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: text})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "FIRST_SCALAR") || strings.Contains(view, "TAIL_SCALAR") || !strings.Contains(view, "additional result content not rendered") {
		t.Fatal("large scalar result did not use a bounded disclosed preview")
	}
}
