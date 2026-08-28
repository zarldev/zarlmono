package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestUtilitySplitPaneUsesOpenSurfaceChrome(t *testing.T) {
	buf := uv.NewScreenBuffer(80, 20)
	layout, ok := drawUtilitySplitPane(buf, buf.Bounds(), 24)
	if !ok {
		t.Fatal("utility split pane did not fit")
	}
	drawOverlayContext(buf, layout, overlayTopBar("utility", []string{"one", "two"}, 0, "summary", layout.Context.Dx()), palette.Border)

	lines := strings.Split(ansi.Strip(buf.Render()), "\n")
	if !strings.Contains(lines[0], "utility") || !strings.Contains(lines[0], "summary") {
		t.Fatalf("utility header missing context: %q", lines[0])
	}
	if !strings.Contains(lines[1], "─") {
		t.Fatalf("utility header missing divider: %q", lines[1])
	}
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("utility surface should not use an outer box:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "│") {
		t.Fatalf("utility body missing nav/detail separator: %q", lines[2])
	}
}
