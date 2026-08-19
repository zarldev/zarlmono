package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestNestedProgramDisclosureReusesChildLayout(t *testing.T) {
	tl, groupIdx, program := nestedProgramTimeline(t, 3, strings.Repeat("result line\n", 80))
	group := tl.items[groupIdx].(*groupItem)

	tl.renderViewport(100, 30)
	initial := program.layout.block.rawLines
	if len(initial) == 0 {
		t.Fatal("nested layout was not populated")
	}

	for range 20 {
		_ = group.toggleLocals(100)
		_ = group.togglerAt(100, 2)
		_ = tl.renderViewport(100, 30)
	}
	if len(program.layout.block.rawLines) != len(initial) {
		t.Fatalf("stable nested layout changed size: got %d want %d", len(program.layout.block.rawLines), len(initial))
	}

	before := program.children[1].version()
	tl.browsing, tl.sel, tl.selLocal = true, groupIdx, 3
	tl.toggleSelected()
	if !program.children[1].expanded || program.children[1].version() == before {
		t.Fatal("nested disclosure did not toggle")
	}
	_ = tl.renderViewport(100, 30)
	if !strings.Contains(strings.Join(program.layout.block.rawLines, "\n"), "result line") {
		t.Fatal("nested layout cache did not invalidate after child toggle")
	}
}

func BenchmarkNestedProgramDisclosure(b *testing.B) {
	tl, groupIdx, program := nestedProgramTimeline(b, 20, strings.Repeat("wrapped result content\n", 100))
	group := tl.items[groupIdx].(*groupItem)
	tl.renderViewport(100, 30)
	locs := group.toggleLocals(100)
	deepest := locs[len(locs)-1]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tl.browsing, tl.sel, tl.selLocal = true, groupIdx, deepest
		tl.toggleSelected()
		_ = tl.renderViewport(100, 30)
		program.children[len(program.children)-1].toggle()
	}
}

func BenchmarkNestedProgramStableLayout(b *testing.B) {
	tl, groupIdx, _ := nestedProgramTimeline(b, 20, strings.Repeat("wrapped result content\n", 100))
	group := tl.items[groupIdx].(*groupItem)
	tl.renderViewport(100, 30)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = group.toggleLocals(100)
		_ = group.togglerAt(100, 10)
	}
}

type testTB interface {
	Helper()
	Fatalf(string, ...any)
}

func nestedProgramTimeline(tb testTB, children int, result string) (*timeline, int, *toolItem) {
	tb.Helper()
	tl := newTimeline()
	tl.startTurn("t", 0)
	tl.startToolWithParent("t", 0, "program", "program", "nested calls", "", 0)
	for i := range children {
		id := "child-" + itoa(i)
		tl.startToolWithParent("t", 0, id, "grep", "needle", "program", i)
		tl.finishTool(id, result, nil, 0, false, tools.Kinds.UNKNOWN)
	}
	tl.finishTool("program", "[]", nil, 0, false, tools.Kinds.UNKNOWN)

	groupIdx := len(tl.items) - 1
	group, ok := tl.items[groupIdx].(*groupItem)
	if !ok {
		tb.Fatalf("last item = %T, want *groupItem", tl.items[groupIdx])
	}
	group.toggle()
	program, ok := group.children[0].(*toolItem)
	if !ok {
		tb.Fatalf("group child = %T, want *toolItem", group.children[0])
	}
	program.toggle()
	tl.viewWidth, tl.viewHeight = 100, 30
	return tl, groupIdx, program
}
