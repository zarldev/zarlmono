package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestNestedToolCompletionInvalidatesAncestors(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t", 0)
	tl.startToolWithParent("t", 0, "program", "program", "nested", "", 0)
	tl.startToolWithParent("t", 0, "child", "grep", "needle", "program", 0)

	groupIdx := len(tl.items) - 1
	group := tl.items[groupIdx].(*groupItem)
	program := group.children[0].(*toolItem)
	group.toggle()
	program.toggle()
	beforeGroup, beforeProgram := group.version(), program.version()

	_ = tl.renderViewport(100, 20)
	tl.finishTool("child", "child completed result", nil, 0, false, tools.Kinds.UNKNOWN)
	if group.version() == beforeGroup || program.version() == beforeProgram {
		t.Fatalf("completion did not propagate versions: group %d→%d program %d→%d",
			beforeGroup, group.version(), beforeProgram, program.version())
	}
	out := ansi.Strip(strings.Join(tl.renderViewport(100, 20), "\n"))
	if !strings.Contains(out, "✓ grep") {
		t.Fatalf("nested completion was not rendered immediately:\n%s", out)
	}
}
