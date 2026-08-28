package tui

import (
	"context"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestServiceDialogUsesSharedDialogRegions(t *testing.T) {
	d := newServiceDialog(context.Background())
	buf := uv.NewScreenBuffer(120, 36)
	d.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())

	for _, want := range []string{
		"local web_search service",
		"SearXNG · optional local backend",
		"refresh status",
		"↑↓ move",
		"enter run",
		"esc close",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("service dialog missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "esc close") != 1 {
		t.Fatalf("service dialog should render one contextual footer:\n%s", out)
	}
}

func TestServiceDialogKeepsFooterAtNarrowHeight(t *testing.T) {
	d := newServiceDialog(context.Background())
	buf := uv.NewScreenBuffer(60, 8)
	d.draw(buf, buf.Bounds())
	lines := strings.Split(ansi.Strip(buf.Render()), "\n")

	if !strings.Contains(lines[0], "local web_search service") {
		t.Fatalf("service dialog title missing: %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-2], "esc close") {
		t.Fatalf("service dialog footer missing at narrow height: %q", lines[len(lines)-2])
	}
}
