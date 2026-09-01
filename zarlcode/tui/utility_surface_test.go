package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestUtilitySplitPaneUsesOpenSurfaceChrome(t *testing.T) {
	out := ansi.Strip(tui.RenderUtilitySurface(80, 20, 24, "utility", []string{"one", "two"}, 0, "summary"))
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "utility") || !strings.Contains(lines[0], "summary") {
		t.Fatalf("header: %q", lines[0])
	}
	if !strings.Contains(lines[1], "─") {
		t.Fatalf("divider: %q", lines[1])
	}
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("outer box:\n%s", out)
	}
	if !strings.Contains(lines[2], "│") {
		t.Fatalf("separator: %q", lines[2])
	}
}
