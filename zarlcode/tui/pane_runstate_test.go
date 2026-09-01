package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestPanesRenderLiveRunState(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "task-1", Depth: 0})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "running") || !strings.Contains(out, "build mode") {
		t.Fatalf("live run state missing from panes:\n%s", out)
	}
	if strings.Contains(out, "ctrl+c quit") {
		t.Errorf("status should not duplicate persistent shortcuts:\n%s", out)
	}

	model, _ = model.Update(teasink.ConversationEndedMsg{TaskID: "task-1", Depth: 0, Reason: runner.TerminalCompleted})
	out = ansi.Strip(model.View().Content)
	if !strings.Contains(out, "idle") {
		t.Fatalf("terminal event left stale live state:\n%s", out)
	}
}
