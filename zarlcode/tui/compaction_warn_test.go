package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAutomaticCompactionStaysQuietAtPressure(t *testing.T) {
	ui := tui.New()
	ui.SetContextWindow(1000)
	ui.SetPressureConfig(1000, 100)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(teasink.IterationCompletedMsg{
		TaskID: "turn", Iter: 1, Usage: &llm.Usage{PromptTokens: 950, TotalTokens: 950},
	})
	out := ansi.Strip(model.View().Content)
	if strings.Contains(out, "compact to free space") {
		t.Fatalf("automatic compaction mode emitted manual warning:\n%s", out)
	}
}
