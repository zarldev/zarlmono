package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func finishedTool(t *testing.T, kind tools.Kind) *toolItem {
	t.Helper()
	tl := newTimeline()
	tl.startTurn("t1", 0)
	tl.startTool("t1", 0, "call1", "read", "missing.go")
	tl.finishTool("call1", "no such file", nil, time.Millisecond, true, kind)
	ref, ok := tl.toolIdx["call1"]
	if !ok || ref.tool == nil {
		t.Fatal("tool not registered")
	}
	return ref.tool
}

// A classified tool failure renders its typed Kind as a badge in the tool
// row, so a validation/not-found/etc. failure reads differently from a raw
// error.
func TestToolFailureRendersKindBadge(t *testing.T) {
	out := ansi.Strip(strings.Join(finishedTool(t, tools.Kinds.NOTFOUND).render(100), "\n"))
	if !strings.Contains(out, "[not_found]") {
		t.Errorf("a classified failure should render its kind badge, got:\n%s", out)
	}
}

// An unclassified failure (Kinds.UNKNOWN) renders no badge — no noisy
// "[unknown]" on every legacy-path error.
func TestToolFailureUnknownKindNoBadge(t *testing.T) {
	out := ansi.Strip(strings.Join(finishedTool(t, tools.Kinds.UNKNOWN).render(100), "\n"))
	if strings.Contains(out, "[unknown]") {
		t.Errorf("an unclassified failure must not render a kind badge, got:\n%s", out)
	}
}
