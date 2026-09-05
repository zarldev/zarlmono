package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestUtilitySurfaceRendersSharedNavigationChrome(t *testing.T) {
	out := ansi.Strip(tui.RenderUtilitySurface(80, 20, 24, "files", []string{"list", "detail"}, 0, "/workspace"))
	for _, want := range []string{"files", "list", "/workspace", "│"} {
		if !strings.Contains(out, want) {
			t.Errorf("surface missing %q:\n%s", want, out)
		}
	}
}
