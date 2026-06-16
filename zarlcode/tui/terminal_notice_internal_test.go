package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func timelineHasNotice(m *UI, substr string) bool {
	for _, it := range m.timeline.items {
		if n, ok := it.(*noticeItem); ok && strings.Contains(ansi.Strip(n.text), substr) {
			return true
		}
	}
	return false
}

// TestHandleRunner_TerminalReasonNotice covers the P0.2 UI half: a turn
// that ends on max_iterations (or cancelled) now leaves a visible
// transcript notice instead of rendering identically to a finished
// answer, while a normal completion stays clean.
func TestHandleRunner_TerminalReasonNotice(t *testing.T) {
	start := func(m *UI, id string) {
		stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
		stepUI(t, m, teasink.ConversationStartedMsg{TaskID: id, Depth: 0, Prompt: "hi"})
	}

	t.Run("max iterations adds a notice", func(t *testing.T) {
		m := New()
		start(m, "t1")
		stepUI(t, m, teasink.ConversationEndedMsg{
			TaskID: "t1", Depth: 0, Reason: runner.TerminalMaxIterations, Iterations: 3,
		})
		if !timelineHasNotice(m, "iteration limit") {
			t.Fatal("expected an iteration-limit notice after a max-iterations turn")
		}
	})

	t.Run("cancelled adds a notice", func(t *testing.T) {
		m := New()
		start(m, "t2")
		stepUI(t, m, teasink.ConversationEndedMsg{
			TaskID: "t2", Depth: 0, Reason: runner.TerminalCancelled, Iterations: 1,
		})
		if !timelineHasNotice(m, "cancelled") {
			t.Fatal("expected a cancelled notice after a cancelled turn")
		}
	})

	t.Run("normal completion stays clean", func(t *testing.T) {
		m := New()
		start(m, "t3")
		stepUI(t, m, teasink.ConversationEndedMsg{
			TaskID: "t3", Depth: 0, Reason: runner.TerminalCompleted, Iterations: 2,
		})
		if timelineHasNotice(m, "iteration limit") || timelineHasNotice(m, "cancelled") {
			t.Fatal("a normal completion should not add a terminal-reason notice")
		}
	})
}
