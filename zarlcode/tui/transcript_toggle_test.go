package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTranscriptToggleExpandsToolGroup(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 30))
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "c", ToolName: "read"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "c", FormattedResult: "RESULT-BODY"})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "read") {
		t.Fatalf("expanded group missing child:\n%s", out)
	}
}
