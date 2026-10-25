package tui_test

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func clickToolLabel(t *testing.T, ui *tui.UI, label string) {
	t.Helper()
	for row, line := range strings.Split(ansi.Strip(ui.View().Content), "\n") {
		if column := strings.Index(line, label); column >= 0 {
			ui.Update(tea.MouseClickMsg{X: column, Y: row, Button: tea.MouseLeft})
			return
		}
	}
	t.Fatalf("missing clickable label %q", label)
}

func TestToolDrawerMouseTargetsFollowChangedRows(t *testing.T) {
	ui := drawerFixture()
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: `{"answer":"RESULT_MARKER"}`})
	clickToolLabel(t, ui, "arguments:")
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "INPUT_MARKER") || !strings.Contains(view, "RESULT_MARKER") {
		t.Fatalf("arguments click selected a different section: %s", view)
	}
	clickToolLabel(t, ui, "result:")
	if strings.Contains(ansi.Strip(ui.View().Content), "RESULT_MARKER") {
		t.Fatal("result click used stale argument row count")
	}
	clickToolLabel(t, ui, "view:")
	clickToolLabel(t, ui, "arguments:")
	if !strings.Contains(ansi.Strip(ui.View().Content), `"path": "INPUT_MARKER"`) {
		t.Fatal("JSON mode mouse action did not survive section changes")
	}
}

func TestResultDrawerHidesScreenshotButLeavesArguments(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "computer_observe", Parameters: map[string]any{"include_screenshot": true}})
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "computer_observe", Parts: []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 16, 8)))}})
	expandFirstTool(ui)
	if !strings.Contains(ansi.Strip(ui.View().Content), "▀") {
		t.Fatal("fixture screenshot missing")
	}
	toolViewKey(ui, 'r')
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "▀") || !strings.Contains(view, "include_screenshot: true") {
		t.Fatalf("result collapse affected input or retained image: %s", view)
	}
	toolViewKey(ui, 'j')
	if view := ansi.Strip(ui.View().Content); strings.Contains(view, "▀") || !strings.Contains(view, `"include_screenshot": true`) {
		t.Fatal("JSON view reopened hidden screenshot")
	}
	toolViewKey(ui, 'r')
	if !strings.Contains(ansi.Strip(ui.View().Content), "▀") {
		t.Fatal("screenshot did not return with result drawer")
	}
}

func TestJSONViewDoesNotReinterpretBashTerminalOutput(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": "printf source"}})
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "bash", FormattedResult: `{"literal_shell_output":false}`})
	expandFirstTool(ui)
	toolViewKey(ui, 'j')
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, `"literal_shell_output"`) || !strings.Contains(view, `"command"`) {
		t.Fatalf("JSON mode changed Bash source/terminal semantics: %s", view)
	}
}
