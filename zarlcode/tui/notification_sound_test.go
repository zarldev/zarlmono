package tui_test

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestCompletedTurnEmitsTerminalBell(t *testing.T) {
	m := tui.New()
	_, _ = m.Update(teasink.ConversationStartedMsg{TaskID: "turn-1", Depth: 0, Prompt: "test"})

	_, cmd := m.Update(teasink.ConversationEndedMsg{
		TaskID: "turn-1",
		Depth:  0,
		Reason: runner.TerminalCompleted,
	})
	if cmd == nil {
		t.Fatal("completed turn returned no terminal bell command")
	}

	raw, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("completed turn command message = %T, want tea.RawMsg", cmd())
	}
	if got := fmt.Sprint(raw.Msg); got != "\a" {
		t.Fatalf("raw terminal output = %q, want one BEL byte", got)
	}
}
