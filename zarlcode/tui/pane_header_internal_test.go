package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The header bar shows app/mode on the left and the active model on the
// right. The model name must be pinned to the right edge — not jammed
// against the mode badge — which an Align() with no width silently fails
// to do.
func TestHeaderPaneRightAlignsModel(t *testing.T) {
	const width = 60
	m := New()
	m.session.Model = "claude-opus"

	buf := uv.NewScreenBuffer(width, 1)
	m.headerPane.Draw(buf, buf.Bounds())
	plain := ansi.Strip(buf.Render())

	if !strings.Contains(plain, "claude-opus") {
		t.Fatalf("model name missing from header:\n%q", plain)
	}
	// Right-aligned: the model's last visible char sits at (or one short of)
	// the right edge. Left of mid-width means it wasn't aligned at all.
	end := strings.Index(plain, "claude-opus") + len("claude-opus")
	if end < width-4 {
		t.Errorf("model name should be right-aligned (end col ~%d), but ended at col %d:\n%q",
			width, end, plain)
	}
}
