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

func TestConversationLifecycleRendersPromptAndPersistentProviderError(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "task-1", Prompt: "fresh prompt", AgentName: "coder"})
	raw := `completion: {"error":{"message":"token budget exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "task-1", Reason: runner.TerminalError, Error: raw})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "fresh prompt") || !strings.Contains(out, "token budget exceeded") || strings.Contains(out, `{"error"`) {
		t.Fatalf("conversation lifecycle output:\n%s", out)
	}
}

func TestLoadSkillLifecycleRendersLoadedSkill(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "task-1"})
	stepUI(t, m, teasink.ToolStartedMsg{TaskID: "task-1", ToolID: "tool-1", ToolName: "skill_load", Parameters: map[string]any{"name": "go-testing"}})
	stepUI(t, m, teasink.ToolCompletedMsg{TaskID: "task-1", ToolID: "tool-1", ToolName: "skill_load", Result: "loaded"})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "go-testing") {
		t.Fatalf("loaded skill absent:\n%s", out)
	}
}
