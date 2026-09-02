package tui_test

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func populatedCockpit(t *testing.T) *tui.UI {
	t.Helper()
	m := tui.New()
	step(t, m, teasink.ConversationStartedMsg{TaskID: "task", Prompt: "test"})
	step(t, m, teasink.ToolStartedMsg{TaskID: "task", ToolID: "read", ToolName: "read"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "task", ToolID: "read", ToolName: "read"})
	step(t, m, teasink.ToolStartedMsg{TaskID: "task", ToolID: "bash", ToolName: "bash"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "task", ToolID: "bash", ToolName: "bash"})
	step(t, m, teasink.IterationCompletedMsg{TaskID: "task", Iter: 1, Usage: &llm.Usage{PromptTokens: 8000, CompletionTokens: 1200, CachedTokens: 2000}})
	return m
}

func TestCockpitLinesFitRenderWidths(t *testing.T) {
	m := populatedCockpit(t)
	for _, w := range []int{52, 56, 80, 120, 200, 280} {
		for i, line := range m.RenderCockpitLines(w) {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width=%d: line %d overflows (%d cols > %d):\n%q", w, i, got, w, ansi.Strip(line))
			}
		}
	}
}

func TestCockpitLinesPanicFreeAtAnyWidth(t *testing.T) {
	m := populatedCockpit(t)
	for w := 1; w <= 80; w++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RenderCockpitLines(%d) panicked: %v", w, r)
				}
			}()
			_ = m.RenderCockpitLines(w)
		}()
	}
}
