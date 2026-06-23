package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSidebar_ShowsRunState(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "do the thing"},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash"},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash", Duration: time.Second},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1, Usage: &llm.Usage{PromptTokens: 120, TotalTokens: 200}},
	)
	for _, want := range []string{"[state]", "context", "plan", "run", "cost", "tools", "running", "provider", "model", "window", "session", "1 calls"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tok/s") {
		t.Errorf("state pane should not include throughput detail:\n%s", out)
	}
}

func TestSidebar_StateCardShowsChangesAndSessionTotals(t *testing.T) {
	m := New()
	m.session.Workspace = "~/proj"
	m.session.Branch = "feat/sidebar"
	m.session.PR = &PRInfo{Number: 4, Title: "Sidebar polish", State: "OPEN", URL: "https://github.com/zarldev/zarlmono/pull/4"}
	m.session.WorkingSet.RecordDiff("a.txt", "@@\n-old\n+new")
	m.session.Run.sessionTurns = 2
	m.session.Run.sessionToolCalls = 5
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	out := ansi.Strip(mm.View().Content)
	for _, want := range []string{"workspace", "~/proj", "branch", "feat/sidebar", "pr", "#4", "changes", "1 files", "1 edits", "session", "2 turns", "5 calls"} {
		if !strings.Contains(out, want) {
			t.Fatalf("state card missing %q:\n%s", want, out)
		}
	}
	workspaceAt := strings.Index(out, "workspace ~/proj")
	branchAt := strings.Index(out, "branch    feat/sidebar")
	prAt := strings.Index(out, "pr        #4")
	if workspaceAt < 0 || branchAt < 0 || prAt < 0 || workspaceAt >= branchAt || branchAt >= prAt {
		t.Fatalf("expected workspace, then branch, then pr ordering:\n%s", out)
	}
}

func drive(t *testing.T, msgs ...tea.Msg) string {
	t.Helper()
	var m tea.Model = New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return ansi.Strip(m.View().Content)
}

func TestSidebar_SectionRulesMeetFrame(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "do the thing"},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1, Usage: &llm.Usage{PromptTokens: 120, TotalTokens: 200}},
	)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "├─[context]") {
			continue
		}
		start := strings.LastIndex(line, "├─[context]")
		if start < 0 {
			t.Fatalf("context section rule should start with a frame joint, got %q", line)
		}
		section := line[start:]
		if !strings.HasSuffix(section, "┤") {
			t.Fatalf("context section rule should meet both frame edges, got %q", line)
		}
		if strings.Contains(line, "│ ├─[context]") || strings.Contains(line, "[context] ") {
			t.Fatalf("context section rule should not keep sidebar gutter spaces, got %q", line)
		}
		return
	}
	t.Fatalf("context section rule missing:\n%s", out)
}

func TestSidebar_RunCompletesToIdle(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "go"},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1},
		teasink.ConversationEndedMsg{TaskID: "t1", Iterations: 1},
	)
	if !strings.Contains(out, "idle") {
		t.Errorf("completed run should read idle:\n%s", out)
	}
}

func TestSidebar_OnlyLastTurnReportsTokPerSec(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "go"},
		// A usage-bearing iteration: the runner sets occupancy (Usage) and
		// this iteration's own usage (Delta) to the same values.
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1,
			Usage: &llm.Usage{
				PromptTokens:     100,
				CompletionTokens: 120,
				TotalTokens:      220,
			},
			Delta: &llm.Usage{
				PromptTokens:     100,
				CompletionTokens: 120,
				TotalTokens:      220,
			},
		},
		teasink.ConversationEndedMsg{
			TaskID: "t1", Iterations: 1, Duration: 2 * time.Second,
			TotalUsage: &llm.Usage{PromptTokens: 100, CompletionTokens: 120, TotalTokens: 220},
		},
	)

	if got := strings.Count(out, "tok/s"); got != 0 {
		t.Fatalf("state pane should keep throughput out of the sidebar, got %d tok/s rows:\n%s", got, out)
	}
}
