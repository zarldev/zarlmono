package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A long diff line must wrap to the available width rather than being silently
// clipped at draw. Regression guard for the diffItem-renders-at-width-0 bug.
func TestRenderDiffContent_WrapsLongLines(t *testing.T) {
	long := "+" + strings.Repeat("x", 200)
	lines := renderDiffContent("@@\n"+long, 40, 0)
	if len(lines) < 2 {
		t.Fatalf("a 201-col diff line should wrap into >=2 rows at width 40, got %d", len(lines))
	}
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w > 40 {
			t.Errorf("row %d width %d exceeds 40: %q", i, w, ln)
		}
	}
}

// A diffItem renders its body to the width it's given (not width 0), so an
// expanded diff inside a group wraps within the pane.
func TestDiffItem_RendersToWidth(t *testing.T) {
	d := &diffItem{path: "a.go", diff: "@@\n+" + strings.Repeat("y", 120), expanded: true}
	for _, ln := range d.render(50) {
		if w := ansi.StringWidth(ln); w > 50 {
			t.Errorf("diff row width %d exceeds 50: %q", w, ln)
		}
	}
}
