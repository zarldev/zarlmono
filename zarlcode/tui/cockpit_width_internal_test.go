package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func populatedCockpit(t *testing.T) *UI {
	t.Helper()
	m := New()
	m.session.Run.Running = true
	m.session.applyToolCompleted(teasink.ToolCompletedMsg{ToolName: "read", Duration: 5 * time.Millisecond})
	m.session.applyToolCompleted(teasink.ToolCompletedMsg{ToolName: "bash", Duration: 200 * time.Millisecond})
	m.session.applyIterationCompleted(teasink.IterationCompletedMsg{
		Iter:  1,
		Usage: &llm.Usage{PromptTokens: 8000, CompletionTokens: 1200, CachedTokens: 2000},
	})
	return m
}

// At the widths the cockpit is actually rendered — the sidebar interior
// (innerW 52; the sidebar is hidden below 160 total) and wider dashboard
// columns — no line may overflow its width.
func TestCockpitLinesFitRenderWidths(t *testing.T) {
	m := populatedCockpit(t)
	for _, w := range []int{52, 56, 80, 120, 200, 280} {
		for i, line := range m.cockpitLines(w) {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width=%d: line %d overflows (%d cols > %d):\n%q", w, i, got, w, ansi.Strip(line))
			}
		}
	}
}

// The builders must never panic, even at transiently tiny widths during a
// resize (a Repeat with a negative count would crash the whole TUI).
func TestCockpitLinesPanicFreeAtAnyWidth(t *testing.T) {
	m := populatedCockpit(t)
	for w := 1; w <= 80; w++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("cockpitLines(%d) panicked: %v", w, r)
				}
			}()
			_ = m.cockpitLines(w)
		}()
	}
}
