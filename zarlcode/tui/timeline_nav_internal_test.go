package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestBrowse_CollapseToggle(t *testing.T) {
	tl := newTimeline()
	tl.addUser("hi")
	tl.appendThinking("t", 0, "secret")
	tl.appendContent("t", 0, "answer")

	// items: user, response, thinking. Up/Down select by item, so the
	// collapsed thinking block can be landed on directly.
	thinkIdx := -1
	for i, it := range tl.items {
		if _, ok := it.(*thinkingItem); ok {
			thinkIdx = i
		}
	}
	if thinkIdx < 0 {
		t.Fatal("no thinking item captured")
	}
	th := tl.items[thinkIdx].(*thinkingItem)
	if th.expanded {
		t.Fatal("thinking should start collapsed")
	}
	tl.sel = thinkIdx
	tl.toggleSelected()
	if !th.expanded {
		t.Error("toggle should expand the selected thinking item")
	}
}

// TestBrowse_SelectsEveryItem verifies Up/Down land on each item — the
// collapsed one-line blocks included — rather than skipping them.
func TestBrowse_SelectsEveryItem(t *testing.T) {
	tl := newTimeline()
	for range 5 {
		tl.addUser("m")
	}
	tl.renderViewport(80, 3) // cache metrics; content taller than the pane

	tl.cursorTop()
	if !tl.browsing || tl.sel != 0 {
		t.Fatalf("cursorTop: browsing=%v sel=%d, want true/0", tl.browsing, tl.sel)
	}
	// Step down through every item.
	for want := 1; want <= 4; want++ {
		tl.cursorDown()
		if tl.sel != want {
			t.Fatalf("cursorDown: sel=%d, want %d", tl.sel, want)
		}
	}
	// One more past the last item resumes follow.
	tl.cursorDown()
	if tl.browsing {
		t.Error("cursorDown past the last item should exit browse (follow tail)")
	}
}

func TestBrowse_SelectsExpandedGroupChildren(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t", 0)
	tl.startToolWithParent("t", 0, "c1", "read", "a.go", "", 0)
	tl.finishTool("c1", "AAA", nil, 0, false, tools.Kinds.UNKNOWN)
	tl.startToolWithParent("t", 0, "c2", "grep", "needle", "", 0)
	tl.finishTool("c2", "BBB", nil, 0, false, tools.Kinds.UNKNOWN)

	groupIdx := len(tl.items) - 1
	g := tl.items[groupIdx].(*groupItem)
	g.expanded = true
	t1 := g.children[0].(*toolItem)
	t2 := g.children[1].(*toolItem)

	tl.viewWidth, tl.viewHeight = 80, 20
	tl.browsing, tl.sel, tl.selLocal = true, groupIdx, 0

	tl.cursorDown()
	if tl.sel != groupIdx || tl.selLocal != 1 {
		t.Fatalf("cursorDown should select first tool row: sel=%d local=%d", tl.sel, tl.selLocal)
	}
	tl.toggleSelected()
	if !t1.expanded || t2.expanded {
		t.Fatalf("toggle on first tool row should expand only first tool: t1=%v t2=%v", t1.expanded, t2.expanded)
	}

	tl.cursorDown()
	if tl.sel != groupIdx || tl.selLocal != 3 {
		t.Fatalf("cursorDown should skip first tool body and select second tool row: sel=%d local=%d", tl.sel, tl.selLocal)
	}
	tl.toggleSelected()
	if !t2.expanded {
		t.Fatal("toggle on second tool row should expand second tool")
	}
}

// TestBrowse_LineScrollReadsTallItem verifies the wheel/page scroll moves by
// lines (so a tall entry can be read through) while keeping a selection.
func TestBrowse_LineScrollReadsTallItem(t *testing.T) {
	tl := newTimeline()
	for range 20 {
		tl.addUser("m")
	}
	tl.renderViewport(80, 5)
	tl.cursorTop() // browse at the top
	start := tl.scrollTop

	tl.wheelDown()
	if tl.scrollTop != start+browseLineStep {
		t.Errorf("wheelDown: scrollTop=%d, want %d", tl.scrollTop, start+browseLineStep)
	}
	tl.wheelUp()
	if tl.scrollTop != start {
		t.Errorf("wheelUp: scrollTop=%d, want %d", tl.scrollTop, start)
	}
	// Selection stays within the viewport after scrolling.
	if tl.sel < 0 || tl.sel >= len(tl.items) {
		t.Errorf("selection went out of range after scroll: %d", tl.sel)
	}
}

func TestBrowse_ScrollToFraction(t *testing.T) {
	tl := newTimeline()
	for range 20 {
		tl.addUser("m")
	}
	tl.renderViewport(80, 5)

	tl.scrollToFraction(0.5)
	if !tl.browsing {
		t.Fatal("scrollToFraction(0.5) should enter browse")
	}
	if tl.scrollTop <= 0 {
		t.Errorf("scrollToFraction(0.5): scrollTop=%d, want > 0", tl.scrollTop)
	}
	tl.scrollToFraction(1.0) // bottom → follow the tail
	if tl.browsing {
		t.Error("scrollToFraction(1.0) should follow the tail")
	}
}

func TestBrowse_RendersRail(t *testing.T) {
	tl := newTimeline()
	for range 6 {
		tl.addUser("line")
	}
	tl.cursorTop()
	lines := tl.renderViewport(80, 4)
	if len(lines) == 0 {
		t.Fatal("browse render empty")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "▎") {
		t.Errorf("selection rail missing from browse view:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTimeline_ComposeFollowsTailBounded(t *testing.T) {
	tl := newTimeline()
	for range 20 {
		tl.addUser("x")
	}
	if lines := tl.renderViewport(80, 5); len(lines) > 5 {
		t.Errorf("tail view should be bounded to height, got %d lines", len(lines))
	}
}
