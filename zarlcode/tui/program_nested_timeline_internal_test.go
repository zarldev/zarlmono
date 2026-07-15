package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestTimelineNestedProgramToolRows(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("task", 0)
	tl.startToolWithParent("task", 0, "outer", "program", "grep, glob", "", 0)
	tl.startToolWithParent("task", 0, "outer/1", "glob", "*.go", "outer", 1)
	tl.startToolWithParent("task", 0, "outer/0", "grep", "TODO", "outer", 0)
	tl.finishTool("outer/0", "grep output", map[string]any{"hits": 1}, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("outer/1", "nope", nil, 2*time.Millisecond, true, tools.Kinds.VALIDATION)
	tl.finishTool("outer", "[]", nil, 3*time.Millisecond, false, tools.Kinds.UNKNOWN)
	if ref := tl.toolIdx["outer"]; ref.group != nil {
		ref.group.expanded = true
	}
	ref := tl.toolIdx["outer"]
	ref.tool.expanded = true
	ref.group.bump()

	out := ansi.Strip(strings.Join(tl.renderViewport(120, 40), "\n"))
	for _, want := range []string{"program  grep, glob", "2/2 calls, 1 failed", "grep  TODO", "glob [validation]  *.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "grep  TODO") > strings.Index(out, "glob [validation]  *.go") {
		t.Fatalf("children not ordered by sequence:\n%s", out)
	}
}

func TestTimelineNestedProgramSuppressesDuplicateCompactResult(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("task", 0)
	tl.startToolWithParent("task", 0, "outer", "program", "grep, glob", "", 0)
	tl.startToolWithParent("task", 0, "outer/0", "grep", "TODO", "outer", 0)
	tl.startToolWithParent("task", 0, "outer/1", "glob", "*.go", "outer", 1)
	tl.finishTool("outer/0", "grep output", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("outer/1", "glob output", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("outer", "[]", nil, 3*time.Millisecond, false, tools.Kinds.UNKNOWN)
	ref := tl.toolIdx["outer"]
	ref.group.expanded = true
	ref.group.bump()
	ref.tool.expanded = true

	out := ansi.Strip(strings.Join(tl.renderViewport(120, 40), "\n"))
	if strings.Count(out, "grep  TODO") != 1 {
		t.Fatalf("nested grep row should render once, not as both child and compact program result:\n%s", out)
	}
	if strings.Count(out, "glob  *.go") != 1 {
		t.Fatalf("nested glob row should render once, not as both child and compact program result:\n%s", out)
	}
	if !strings.Contains(out, "2/2 calls") {
		t.Fatalf("program parent should still show child summary:\n%s", out)
	}
}

func TestTimelineNestedProgramHidesChildRowsUntilProgramExpanded(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("task", 0)
	tl.startToolWithParent("task", 0, "outer", "program", "grep, glob", "", 0)
	tl.startToolWithParent("task", 0, "outer/0", "grep", "TODO", "outer", 0)
	tl.startToolWithParent("task", 0, "outer/1", "glob", "*.go", "outer", 1)

	tl.finishTool("outer/0", "grep output", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("outer/1", "glob output", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("outer", "[]", nil, 3*time.Millisecond, false, tools.Kinds.UNKNOWN)
	ref := tl.toolIdx["outer"]
	ref.group.expanded = true

	collapsed := ansi.Strip(strings.Join(tl.renderViewport(120, 40), "\n"))
	if !strings.Contains(collapsed, "program  grep, glob") || !strings.Contains(collapsed, "2/2 calls") {
		t.Fatalf("program header should show summary while collapsed:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "grep  TODO") || strings.Contains(collapsed, "glob  *.go") {
		t.Fatalf("program children should be hidden until program row expands:\n%s", collapsed)
	}

	ref.tool.expanded = true
	ref.group.bump()
	open := ansi.Strip(strings.Join(tl.renderViewport(120, 40), "\n"))
	if !strings.Contains(open, "grep  TODO") || !strings.Contains(open, "glob  *.go") {
		t.Fatalf("expanded program row should show nested child calls:\n%s", open)
	}
	if strings.Contains(open, "grep output") || strings.Contains(open, "glob output") {
		t.Fatalf("child call results should stay hidden until each child row expands:\n%s", open)
	}

	ref.tool.children[0].expanded = true
	ref.group.bump()
	childOpen := ansi.Strip(strings.Join(tl.renderViewport(120, 40), "\n"))
	if !strings.Contains(childOpen, "grep output") {
		t.Fatalf("expanded child row should show its result:\n%s", childOpen)
	}
}
