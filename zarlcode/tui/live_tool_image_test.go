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

func TestFinalImageResultReplacesLiveTextWithoutGeometryMismatch(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "image", ToolID: "image", ToolName: "computer_observe", Parameters: map[string]any{"include_screenshot": true}})
	ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "image", ToolID: "image", Text: "PROVISIONAL_IMAGE_PROGRESS\n"})
	expandFirstTool(ui)
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "image", ToolID: "image", ToolName: "computer_observe", Parts: []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 16, 8)))}})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "▀") || strings.Contains(view, "PROVISIONAL_IMAGE_PROGRESS") || strings.Contains(view, "live output:") {
		t.Fatalf("final attachment did not replace preview body: %s", view)
	}
	toolViewKey(ui, 'r')
	if strings.Contains(ansi.Strip(ui.View().Content), "▀") {
		t.Fatal("hidden final image retained an overlay")
	}
}
