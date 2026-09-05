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

func TestAgentActivityScreenShowsTargetAndLiveDetail(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 150, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "root", Prompt: "review the change"})
	stepUI(t, m, teasink.ToolStartedMsg{
		TaskID: "root", ToolID: "spawn", ToolName: "agent_spawn",
		Parameters: map[string]any{"agent": "reviewer", "prompt": "review auth changes"},
	})
	stepUI(t, m, teasink.ConversationStartedMsg{
		TaskID: "child", Depth: 1, ParentToolCallID: "spawn", AgentName: "reviewer",
		Provider: "anthropic", Model: "claude-sonnet", Prompt: "review auth changes",
	})
	stepUI(t, m, teasink.ContentMsg{TaskID: "child", Depth: 1, Delta: "Found a token validation regression."})

	timeline := ansi.Strip(m.View().Content)
	if !strings.Contains(timeline, "[running]  reviewer · anthropic/claude-sonnet: review auth changes") {
		t.Fatalf("timeline agent row should lead with status and show its target:\n%s", timeline)
	}

	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'})
	activity := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"agent activity",
		"1 delegated · 1 running",
		"[running]  reviewer · anthropic/claude-sonnet",
		"target: anthropic/claude-sonnet",
		"task: child",
		"assignment",
		"review auth changes",
		"activity",
		"Found a token validation regression.",
		"ctrl+a / esc close",
	} {
		if !strings.Contains(activity, want) {
			t.Fatalf("agent activity missing %q:\n%s", want, activity)
		}
	}

	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "child", Depth: 1, Reason: runner.TerminalCompleted})
	completed := ansi.Strip(m.View().Content)
	if !strings.Contains(completed, "[complete]  reviewer · anthropic/claude-sonnet") {
		t.Fatalf("completed agent status did not update in place:\n%s", completed)
	}

	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'})
	if closed := ansi.Strip(m.View().Content); strings.Contains(closed, "agent activity") {
		t.Fatalf("ctrl+a should close the agent activity screen:\n%s", closed)
	}
}
