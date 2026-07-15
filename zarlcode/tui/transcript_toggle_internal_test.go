package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// A tool row carries its own [+]/[-] and hides its result when collapsed,
// independent of the group. read results render as syntax-highlighted code blocks
// via the shared content renderer, so the original result text may be ANSI-coloured.
func TestToolItem_ToggleHidesResult(t *testing.T) {
	ti := &toolItem{name: "read", arg: "a.go", state: toolOK, result: "RESULT-BODY", expanded: true}

	open := strings.Join(ti.render(80), "\n")
	if !strings.Contains(open, "[-]") {
		t.Fatalf("expanded tool should show [-]:\n%s", open)
	}
	// read results render as code blocks; the result may be wrapped in ANSI.
	if !strings.Contains(ansi.Strip(open), "RESULT-BODY") {
		t.Fatalf("expanded tool should show its result (possibly ANSI-coloured):\n%s", open)
	}
	ti.toggle()
	shut := strings.Join(ti.render(80), "\n")
	if !strings.Contains(shut, "[+]") || strings.Contains(ansi.Strip(shut), "RESULT-BODY") {
		t.Fatalf("collapsed tool should show [+] and hide its result:\n%s", shut)
	}
}

// group.togglerAt resolves the child header line to a toggler that collapses
// that child (and only that child).
func TestGroup_TogglerAtChildCollapsesOnlyThatChild(t *testing.T) {
	g := &groupItem{kind: groupTools, expanded: true}
	a := &toolItem{name: "read", state: toolOK, result: "AAA", expanded: true}
	b := &toolItem{name: "grep", state: toolOK, result: "BBB", expanded: true}
	g.add(a)
	g.add(b)

	// Child a's header is the group's local line 1 (line 0 is the group header).
	tg := g.togglerAt(80, 1)
	if tg == nil {
		t.Fatal("expected a toggler at the first child's header line")
	}
	tg.toggle()
	if a.expanded {
		t.Error("first child should be collapsed after toggling its header line")
	}
	if !b.expanded {
		t.Error("toggling the first child must not affect its sibling")
	}

	// Line 0 still toggles the whole group.
	if got := g.togglerAt(80, 0); got != toggler(g) {
		t.Errorf("local line 0 should toggle the group itself, got %T", got)
	}
}

// A sub-agent's togglerAt delegates into nested group children so tool rows
// inside an expanded sub-agent are clickable.
func TestSubAgentItem_TogglerAtDelegatesToNestedTool(t *testing.T) {
	sa := newSubAgentItem(1, "helper", "do work", "t1")
	sa.expanded = true

	g := &groupItem{depth: 2, kind: groupTools, nested: true, expanded: true}
	tool := &toolItem{name: "read", arg: "a.go", state: toolOK, result: "RESULT", expanded: true}
	g.add(tool)
	sa.children = append(sa.children, g)
	sa.toolIdx["c1"] = toolRef{group: g, tool: tool}

	// line 0 = sub-agent header
	tg := sa.togglerAt(80, 0)
	if tg == nil {
		t.Fatal("expected toggler at sub-agent header")
	}
	tg.toggle()
	if sa.expanded {
		t.Error("sub-agent should be collapsed")
	}
	sa.expanded = true

	// line 1 = group header
	tg = sa.togglerAt(80, 1)
	if tg == nil {
		t.Fatal("expected toggler at group header")
	}
	tg.toggle()
	if g.expanded {
		t.Error("group should be collapsed")
	}
	g.expanded = true

	// line 2 = tool header (nested inside expanded group)
	tg = sa.togglerAt(80, 2)
	if tg == nil {
		t.Fatal("expected toggler at tool header")
	}
	tg.toggle()
	if tool.expanded {
		t.Error("tool should be collapsed")
	}
}

// A click resolved through toggleAtViewportLine flips the group header on the
// line under the cursor.
func TestToggleAtViewportLine_TogglesGroupHeader(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t", 0)
	tl.startToolWithParent("t", 0, "c1", "read", "a.go", "", 0)
	tl.finishTool("c1", "line one\nline two", nil, time.Millisecond, false, tools.Kinds.UNKNOWN)

	g := tl.items[len(tl.items)-1].(*groupItem)
	tl.browsing, tl.sel = true, len(tl.items)-1
	tl.renderViewport(80, 20) // records visItem/visLocal

	vi := -1
	for i := range tl.visItem {
		if tl.visItem[i] == len(tl.items)-1 && tl.visLocal[i] == 0 {
			vi = i
			break
		}
	}
	if vi < 0 {
		t.Fatal("group header viewport line not found")
	}
	before := g.expanded
	if !tl.toggleAtViewportLine(vi) {
		t.Fatal("expected the group header line to toggle")
	}
	if g.expanded == before {
		t.Error("group expanded state should have flipped")
	}
}
