package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// When the theme grid is taller than the detail height (e.g. a narrow terminal
// → one column → many rows), the cursor's row must stay visible: detailLines
// windows the grid rows around the cursor's row instead of drawing from row 0.
func TestThemeGallery_DetailLinesWindowsCursorRow(t *testing.T) {
	g := &themeGallery{}
	for i := range 40 {
		g.names = append(g.names, fmt.Sprintf("theme-%02d", i))
	}
	g.cursor = len(g.names) - 1 // focus the last theme

	// width == themeCellW → a single column, so 40 rows; height 10 overflows.
	out := ansi.Strip(strings.Join(g.detailLines(themeCellW, 10), "\n"))
	if !strings.Contains(out, "theme-39") {
		t.Errorf("focused theme must stay visible when the grid overflows:\n%s", out)
	}
	if strings.Contains(out, "theme-00") {
		t.Errorf("top of an overflowing grid should be windowed out:\n%s", out)
	}
}

// A grid that fits the detail height renders every theme from the top.
func TestThemeGallery_DetailLinesFitsUnwindowed(t *testing.T) {
	g := &themeGallery{names: []string{"alpha", "beta", "gamma"}}
	out := ansi.Strip(strings.Join(g.detailLines(themeCellW, 10), "\n"))
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, want) {
			t.Errorf("a grid that fits should render all themes, missing %q:\n%s", want, out)
		}
	}
}
