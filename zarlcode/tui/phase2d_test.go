package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	teasink "github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimeline_ToolShowsArgHint(t *testing.T) {
	out := drive(t,
		teasink.ToolStartedMsg{
			TaskID: "t1", ToolID: "c1", ToolName: "bash",
			Parameters: map[string]any{"command": "go build ./..."},
		},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash", Duration: time.Second},
	)
	if !strings.Contains(out, "tools (1)") {
		t.Errorf("tool group summary missing:\n%s", out)
	}
}

func TestTimeline_SubAgentFrameNested(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Depth: 0, Prompt: "main task"},
		teasink.ConversationStartedMsg{TaskID: "s1", Depth: 1, Prompt: "sub task"},
		teasink.ContentMsg{TaskID: "s1", Depth: 1, Delta: "working on it"},
	)
	// Sub-agent now renders as a collapsible block (collapsed by default).
	if !strings.Contains(out, "[+]") || !strings.Contains(out, "sub task") {
		t.Errorf("sub-agent collapsible block missing:\n%s", out)
	}
	// Content is hidden while collapsed.
	if strings.Contains(out, "working on it") {
		t.Errorf("sub-agent content should be hidden while collapsed:\n%s", out)
	}
}

func TestTimeline_SubAgentExpandedShowsContent(t *testing.T) {
	var m tea.Model = tui.New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m, _ = m.Update(teasink.ConversationStartedMsg{TaskID: "t1", Depth: 0, Prompt: "main task"})
	m, _ = m.Update(teasink.ConversationStartedMsg{TaskID: "s1", Depth: 1, AgentName: "helper", Prompt: "sub task"})
	m, _ = m.Update(teasink.ContentMsg{TaskID: "s1", Depth: 1, Delta: "working on it"})

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "[+] helper: sub task") {
		t.Fatalf("collapsed sub-agent missing:\n%s", out)
	}

	// Enter browse mode and expand the sub-agent block.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	out = ansi.Strip(m.View().Content)
	if !strings.Contains(out, "[-] helper: sub task") {
		t.Fatalf("expanded sub-agent missing [-]:\n%s", out)
	}
	if !strings.Contains(out, "working on it") {
		t.Fatalf("expanded sub-agent should show content:\n%s", out)
	}
}
