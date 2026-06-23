package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestTimeline_FoldsRun(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "fix the parser bug"},
		teasink.ContentMsg{TaskID: "t1", Delta: "Reading the "},
		teasink.ContentMsg{TaskID: "t1", Delta: "parser first."},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "read_file"},
		teasink.ToolCompletedMsg{
			TaskID: "t1", ToolID: "c1", ToolName: "read_file",
			FormattedResult: "package parser\n...more...", Duration: 1200 * time.Millisecond,
		},
	)
	// tools collapse into a per-iteration group summary by default.
	for _, want := range []string{"fix the parser bug", "parser first.", "tools (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("timeline missing %q in:\n%s", want, out)
		}
	}
}

func TestTimeline_ToolFailureShows(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "run tests"},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash"},
		teasink.ToolFailedMsg{
			TaskID: "t1", ToolID: "c1", ToolName: "bash",
			Error: "exit status 1", Duration: 300 * time.Millisecond,
		},
	)
	if !strings.Contains(out, "tools (1)") || !strings.Contains(out, "1 failed") {
		t.Errorf("failed-tool group summary missing in:\n%s", out)
	}
}

func TestTimeline_ToolEffectSummaryRendersWhenExpanded(t *testing.T) {
	var m tea.Model = New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	effect := tools.NewFileEffect(tools.FileModify, "pkg/foo.go")
	m, _ = m.Update(teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "edit foo"})
	m, _ = m.Update(teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "edit"})
	m, _ = m.Update(teasink.ToolCompletedMsg{
		TaskID: "t1", ToolID: "c1", ToolName: "edit", FormattedResult: "edited", Effects: []tools.Effect{effect},
	})

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})   // browse mode
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand selected tools group
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "modified pkg/foo.go") {
		t.Fatalf("effect summary missing from expanded tool row:\n%s", out)
	}
}

func TestTimeline_ContentResumesAfterToolAsNewItem(t *testing.T) {
	out := drive(t,
		teasink.ContentMsg{TaskID: "t1", Delta: "before tool"},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "grep"},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "c1", ToolName: "grep", Duration: time.Millisecond},
		teasink.ContentMsg{TaskID: "t1", Delta: "after tool"},
	)
	if !strings.Contains(out, "before tool") || !strings.Contains(out, "after tool") {
		t.Errorf("content around tool missing in:\n%s", out)
	}
}

func TestTimeline_LoadedSkillAndAgentStayInNestedToolGroup(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "use helpers"},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "s1", ToolName: "load_skill", Parameters: map[string]any{"name": "go-testing"}},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "s1", ToolName: "load_skill", Duration: time.Millisecond},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "a1", ToolName: "spawn_agent", Parameters: map[string]any{"agent": "go-code-reviewer", "prompt": "review"}},
		teasink.ConversationStartedMsg{TaskID: "child1", Depth: 1, AgentName: "go-code-reviewer", Prompt: "review"},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "a1", ToolName: "spawn_agent", Duration: time.Millisecond},
	)
	if strings.Contains(out, "loaded skill") || strings.Contains(out, "loaded agent") {
		t.Errorf("loaded skill/agent should not appear as inline transcript notices:\n%s", out)
	}
	// Sub-agent now renders as a collapsible block instead of a "⤷ review" notice.
	// Skills appear as a collapsible section under the assistant turn.
	for _, want := range []string{"use helpers", "tools (2)", "[+] go-code-reviewer: review", "[+] skills (1): go-testing"} {
		if !strings.Contains(out, want) {
			t.Errorf("timeline missing %q in:\n%s", want, out)
		}
	}
}

func TestTimeline_PlanUpdateIsCollapsibleInline(t *testing.T) {
	p := code.Plan{
		Steps: []code.PlanStep{
			{Text: "read the failing test", Status: code.StepStatuses.COMPLETED},
			{Text: "patch the handler", Status: code.StepStatuses.PENDING},
		},
		Explanation: "seeded initial plan",
	}
	var m tea.Model = New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m, _ = m.Update(teasink.PlanUpdatedMsg{Plan: p})

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "[+] plan updated · 1/2 done") {
		t.Fatalf("collapsed plan update summary missing:\n%s", out)
	}
	if strings.Count(out, "read the failing test") > 1 {
		t.Fatalf("collapsed plan update should hide timeline step details:\n%s", out)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})   // browse mode
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand selected plan row
	out = ansi.Strip(m.View().Content)
	for _, want := range []string{"[-] plan updated · 1/2 done", "read the failing test", "latest update: seeded initial plan"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded plan update missing %q:\n%s", want, out)
		}
	}
}

func TestTimeline_PlanUpdatesReuseInlineRow(t *testing.T) {
	first := code.Plan{
		Steps: []code.PlanStep{
			{Text: "read the failing test", Status: code.StepStatuses.COMPLETED},
			{Text: "patch the handler", Status: code.StepStatuses.PENDING},
		},
		Explanation: "seeded initial plan",
	}
	latest := code.Plan{
		Steps: []code.PlanStep{
			{Text: "read the failing test", Status: code.StepStatuses.COMPLETED},
			{Text: "patch the handler", Status: code.StepStatuses.COMPLETED},
			{Text: "run the tests", Status: code.StepStatuses.PENDING},
		},
		Explanation: "patched handler",
	}

	out := drive(t,
		teasink.PlanUpdatedMsg{Plan: first},
		teasink.PlanUpdatedMsg{Plan: latest},
	)
	if got := strings.Count(out, "plan updated ·"); got != 1 {
		t.Fatalf("plan updates should reuse a single timeline row, got %d rows:\n%s", got, out)
	}
	if !strings.Contains(out, "[+] plan updated · 2/3 done") {
		t.Fatalf("latest plan summary missing:\n%s", out)
	}
	if strings.Contains(out, "[+] plan updated · 1/2 done") {
		t.Fatalf("stale plan summary should have been replaced:\n%s", out)
	}
}
