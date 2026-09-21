package tui_test

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Feed real public messages and check both the transcript and cockpit, not
// private activity fields or a second test-only implementation of the resolver.
func newActivityUI() *tui.UI {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "work"})
	return ui
}

func assertLiveActivity(t *testing.T, ui *tui.UI, want string) {
	t.Helper()
	out := ansi.Strip(ui.View().Content)
	if n := strings.Count(out, "⠋ "+want); n != 2 {
		t.Fatalf("activity %q appeared on %d surfaces, want transcript and cockpit:\n%s", want, n, out)
	}
}

func TestActivityStreamingBoundaries(t *testing.T) {
	ui := newActivityUI()
	steps := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"empty content", teasink.ContentMsg{TaskID: "turn"}, "working"},
		{"reasoning", teasink.ThinkingMsg{TaskID: "turn", Delta: "reason"}, "thinking"},
		{"content", teasink.ContentMsg{TaskID: "turn", Delta: "answer"}, "responding"},
		{"empty reasoning", teasink.ThinkingMsg{TaskID: "turn"}, "responding"},
		{"iteration", teasink.IterationCompletedMsg{TaskID: "turn", Iter: 2}, "working"},
		{"next reasoning", teasink.ThinkingMsg{TaskID: "turn", Delta: "reason"}, "thinking"},
		{"attempt settled", teasink.ProviderAttemptSettledMsg{TaskID: "turn", Attempt: 1}, "working"},
		{"fresh response", teasink.ContentMsg{TaskID: "turn", Delta: "answer"}, "responding"},
		{"duplicate settlement", teasink.ProviderAttemptSettledMsg{TaskID: "turn", Attempt: 1}, "responding"},
		{"tool supersedes stream", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"}, "running read"},
		{"stream during tool", teasink.ThinkingMsg{TaskID: "turn", Delta: "buffered"}, "running read"},
		{"no stale stream after tool", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"}, "working"},
		{"fresh reasoning", teasink.ThinkingMsg{TaskID: "turn", Delta: "reason"}, "thinking"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			ui.Update(step.msg)
			assertLiveActivity(t, ui, step.want)
		})
	}
}

func TestActivityParallelToolsAndWorkspaceWaits(t *testing.T) {
	ui := newActivityUI()
	steps := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"first", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "reused", ExecutionID: "one", ToolName: "grep"}, "running grep"},
		{"duplicate", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "reused", ExecutionID: "one", ToolName: "grep"}, "running grep"},
		{"second with same provider id", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "reused", ExecutionID: "two", ToolName: "read"}, "running 2 tools"},
		{"partial blocking", teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "one"}, "running 2 tools"},
		{"all blocked", teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "two"}, "waiting for workspace"},
		{"one resumes", teasink.WorkspaceWaitEndedMsg{TaskID: "turn", ExecutionID: "two", Outcome: tools.WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED}, "running 2 tools"},
		{"second finishes first", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "reused", ExecutionID: "two", ToolName: "read"}, "waiting for workspace"},
		{"unmatched completion", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "unknown", ToolName: "read"}, "waiting for workspace"},
		{"cancelled wait", teasink.WorkspaceWaitEndedMsg{TaskID: "turn", ExecutionID: "one", Outcome: tools.WorkspaceWaitOutcomes.WORKSPACEWAITCANCELLED}, "working"},
		{"failure after cancellation", teasink.ToolFailedMsg{TaskID: "turn", ToolID: "reused", ExecutionID: "one", ToolName: "grep", Error: "cancelled"}, "working"},
		{"late wait does not resurrect", teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "one"}, "working"},
		{"legacy id fallback", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "legacy", ToolName: "bash"}, "running bash"},
		{"failure", teasink.ToolFailedMsg{TaskID: "turn", ToolID: "legacy", ToolName: "bash", Error: "exit 1"}, "working"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			ui.Update(step.msg)
			assertLiveActivity(t, ui, step.want)
		})
	}
}

func TestActivityNestedTools(t *testing.T) {
	ui := newActivityUI()
	steps := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"wrapper", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "p", ExecutionID: "parent", ToolName: "program"}, "running program"},
		{"uncorrelated child", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "orphan", ExecutionID: "orphan", ParentExecutionID: "unknown", ToolName: "glob"}, "running program"},
		{"one child", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "one", ExecutionID: "one", ParentExecutionID: "parent", ToolName: "grep"}, "running grep"},
		{"blocked child is not blocked wrapper", teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ToolID: "one", ExecutionID: "one"}, "running program"},
		{"child resumes", teasink.WorkspaceWaitEndedMsg{TaskID: "turn", ToolID: "one", ExecutionID: "one"}, "running grep"},
		{"ambiguous children", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "two", ExecutionID: "two", ParentExecutionID: "parent", ToolName: "read"}, "running program"},
		{"first child finishes", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "one", ExecutionID: "one", ParentExecutionID: "parent", ToolName: "grep"}, "running read"},
		{"independent root counts once", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "root", ExecutionID: "root", ToolName: "ls"}, "running 2 tools"},
		{"root finishes", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "root", ExecutionID: "root", ToolName: "ls"}, "running read"},
		{"wrapper failure retires children", teasink.ToolFailedMsg{TaskID: "turn", ToolID: "p", ExecutionID: "parent", ToolName: "program", Error: "timeout"}, "working"},
		{"late child completion", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "two", ExecutionID: "two", ParentExecutionID: "parent", ToolName: "read"}, "working"},
		{"late child start", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "three", ExecutionID: "three", ParentExecutionID: "parent", ToolName: "read"}, "working"},
		{"legacy wrapper", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "legacy", ToolName: "parallel"}, "running parallel"},
		{"legacy child", teasink.ToolStartedMsg{TaskID: "turn", ToolID: "child", ParentToolID: "legacy", ToolName: "glob"}, "running glob"},
		{"legacy child finished", teasink.ToolCompletedMsg{TaskID: "turn", ToolID: "child", ParentToolID: "legacy", ToolName: "glob"}, "running parallel"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			ui.Update(step.msg)
			assertLiveActivity(t, ui, step.want)
		})
	}
}

func TestActivityIgnoresOtherTasks(t *testing.T) {
	for _, other := range []struct {
		name, task string
		depth      int
	}{
		{"stale", "old", 0},
		{"child", "child", 1},
		{"wrong depth", "turn", 1},
	} {
		t.Run(other.name, func(t *testing.T) {
			ui := newActivityUI()
			ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, AgentName: "tester"})
			ui.Update(teasink.ThinkingMsg{TaskID: "turn", Delta: "reason"})
			for _, msg := range []tea.Msg{
				teasink.ContentMsg{TaskID: other.task, Depth: other.depth, Delta: "child response"},
				teasink.IterationCompletedMsg{TaskID: other.task, Depth: other.depth, Iter: 2},
				teasink.ProviderAttemptSettledMsg{TaskID: other.task, Depth: other.depth, Attempt: 1},
				teasink.ToolStartedMsg{TaskID: other.task, Depth: other.depth, ToolID: "other", ExecutionID: "other", ToolName: "read"},
			} {
				ui.Update(msg)
				assertLiveActivity(t, ui, "thinking")
			}
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "own", ExecutionID: "own", ToolName: "grep"})
			for _, msg := range []tea.Msg{
				teasink.WorkspaceWaitStartedMsg{TaskID: other.task, Depth: other.depth, ExecutionID: "own"},
				teasink.ToolCompletedMsg{TaskID: other.task, Depth: other.depth, ToolID: "own", ExecutionID: "own", ToolName: "grep"},
				teasink.ToolFailedMsg{TaskID: other.task, Depth: other.depth, ToolID: "own", ExecutionID: "own", ToolName: "grep", Error: "error"},
				teasink.ThinkingMsg{TaskID: other.task, Depth: other.depth, Delta: "reason"},
				teasink.ConversationEndedMsg{TaskID: other.task, Depth: other.depth, Reason: runner.TerminalCompleted},
			} {
				ui.Update(msg)
				assertLiveActivity(t, ui, "running grep")
			}
		})
	}
}

func TestActivityTerminalAndNewTurnReset(t *testing.T) {
	for _, reason := range []runner.TerminalReason{runner.TerminalCompleted, runner.TerminalCancelled, runner.TerminalError, runner.TerminalMaxIterations} {
		t.Run(string(reason), func(t *testing.T) {
			ui := newActivityUI()
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "old", ExecutionID: "old", ToolName: "grep"})
			ui.Update(teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "old"})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: reason, Error: "test error"})
			out := ansi.Strip(ui.View().Content)
			if !strings.Contains(out, "○ idle") || strings.Contains(out, "⠋ ") {
				t.Fatalf("terminal activity not idle:\n%s", out)
			}
			ui.Update(teasink.ConversationStartedMsg{TaskID: "next"})
			ui.Update(teasink.IterationCompletedMsg{TaskID: "next", Iter: 1})
			assertLiveActivity(t, ui, "working")
			ui.Update(teasink.ThinkingMsg{TaskID: "next", Delta: "reason"})
			for _, msg := range []tea.Msg{
				teasink.ToolStartedMsg{TaskID: "turn", ToolID: "old", ExecutionID: "old", ToolName: "grep"},
				teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "old"},
				teasink.ContentMsg{TaskID: "turn", Delta: "late"},
				teasink.ConversationEndedMsg{TaskID: "turn", Reason: reason},
			} {
				ui.Update(msg)
				assertLiveActivity(t, ui, "thinking")
			}
		})
	}
}

func TestActivityIndependentOfWorkflowMode(t *testing.T) {
	ui := newActivityUI()
	ui.Update(teasink.ThinkingMsg{TaskID: "turn", Delta: "reason"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	out := ansi.Strip(ui.View().Content)
	if !strings.Contains(out, "plan mode") {
		t.Fatalf("expected plan mode:\n%s", out)
	}
	assertLiveActivity(t, ui, "thinking")
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"})
	assertLiveActivity(t, ui, "running read")
}

func TestActivityLongNamesPreserveViewport(t *testing.T) {
	for _, width := range []int{32, 48, 80, 104, 120, 160, 240} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			ui := newActivityUI()
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "long", ToolName: strings.Repeat("LONG工具", 20)})
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 60})
			out := ansi.Strip(ui.View().Content)
			if width >= 104 && strings.Count(out, "⠋ running long") != 2 {
				t.Fatalf("expected lowercase activity in transcript and narrow sidebar:\n%s", out)
			}
			for line := range strings.SplitSeq(out, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("rendered row exceeds width %d: %s", width, line)
				}
			}
			var header string
			for line := range strings.SplitSeq(out, "\n") {
				if strings.Contains(line, "ƶ") {
					header = line
					break
				}
			}
			if !strings.Contains(header, "follow 100%") || ansi.StringWidth(header) > width {
				t.Fatalf("viewport not preserved at width %d:\n%s", width, out)
			}
			if strings.Contains(header, "LONG") || strings.Contains(header, strings.Repeat("long工具", 4)) {
				t.Fatalf("activity name not lowercase and bounded: %s", header)
			}
		})
	}
}
