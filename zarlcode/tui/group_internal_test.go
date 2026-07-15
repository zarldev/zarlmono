package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestGroup_ToolsGroupedPerIteration(t *testing.T) {
	tl := newTimeline()
	tl.startToolWithParent("t", 0, "c1", "read", "a.go", "", 0)
	tl.startToolWithParent("t", 0, "c2", "grep", "foo", "", 0)
	tl.finishTool("c1", "ok", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.finishTool("c2", "ok", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)

	g := tl.curTools
	if g == nil || len(g.children) != 2 {
		t.Fatalf("want one open tool group with 2 children, got %v", g)
	}
	if g.expanded {
		t.Error("group should be collapsed by default")
	}

	tl.closeGroups() // iteration boundary
	if !g.closed || tl.curTools != nil {
		t.Error("closeGroups should freeze and clear the current group")
	}
	tl.startToolWithParent("t", 0, "c3", "bash", "x", "", 0)
	if tl.curTools == g {
		t.Error("a new iteration should open a fresh group")
	}
}

func TestGroup_CollapsedSummaryVsExpanded(t *testing.T) {
	g := &groupItem{kind: groupTools}
	g.add(&toolItem{name: "read", state: toolOK})
	g.add(&toolItem{name: "grep", state: toolFailed})

	collapsed := strings.Join(g.render(80), "\n")
	if !strings.Contains(collapsed, "tools (2)") || !strings.Contains(collapsed, "1 failed") {
		t.Errorf("collapsed summary wrong: %q", collapsed)
	}
	if strings.Contains(collapsed, "read") {
		t.Error("collapsed group should hide children")
	}

	g.toggle()
	expanded := strings.Join(g.render(80), "\n")
	if !strings.Contains(expanded, "read") || !strings.Contains(expanded, "grep") {
		t.Errorf("expanded group should show children:\n%s", expanded)
	}
}

func TestTool_KeepsFullMultiLineResult(t *testing.T) {
	tl := newTimeline()
	tl.startToolWithParent("t", 0, "c1", "bash", "echo", "", 0)
	tl.finishTool("c1", "line one\nline two\nline three", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)

	g := tl.curTools
	g.expanded = true
	g.children[0].(*toolItem).expanded = true // tools collapse by default; open this row
	out := strings.Join(g.render(80), "\n")
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-line tool result missing %q (was it first-line-truncated?):\n%s", want, out)
		}
	}
}

func TestTool_RendersEffectSummary(t *testing.T) {
	tl := newTimeline()
	tl.startToolWithParent("t", 0, "c1", "edit", "pkg/foo.go", "", 0)
	tl.finishTool("c1", "edited", nil, time.Millisecond, false, tools.Kinds.UNKNOWN, "modified pkg/foo.go")

	g := tl.curTools
	g.expanded = true
	out := strings.Join(g.render(120), "\n")
	if !strings.Contains(out, "modified pkg/foo.go") {
		t.Fatalf("effect summary missing from expanded tool row:\n%s", out)
	}
}

// Reasoning that arrives before and after a tool call (across iterations)
// must accumulate in a single per-turn thinking block, not spawn a second.
func TestThinking_SingleBlockPerTurn(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t", 0)
	tl.appendThinking("t", 0, "before the tool")
	tl.startToolWithParent("t", 0, "c1", "read", "a.go", "", 0)
	tl.finishTool("c1", "ok", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)
	tl.closeGroups() // iteration boundary
	tl.appendThinking("t", 0, "after the tool")
	tl.appendContent("t", 0, "the answer")

	var think []*thinkingItem
	for _, it := range tl.items {
		if ti, ok := it.(*thinkingItem); ok {
			think = append(think, ti)
		}
	}
	if len(think) != 1 {
		t.Fatalf("want exactly one thinking block, got %d", len(think))
	}
	for _, want := range []string{"before the tool", "after the tool"} {
		if !strings.Contains(think[0].text, want) {
			t.Errorf("thinking block missing %q: %q", want, think[0].text)
		}
	}
}

func TestGroup_EditSummaryPluralizes(t *testing.T) {
	g := &groupItem{kind: groupEdits}
	g.add(&diffItem{path: "a.go"})
	if got := g.summary(); got != "edits (1 file)" {
		t.Errorf("summary = %q, want %q", got, "edits (1 file)")
	}
	g.add(&diffItem{path: "b.go"})
	if got := g.summary(); got != "edits (2 files)" {
		t.Errorf("summary = %q, want %q", got, "edits (2 files)")
	}
}
