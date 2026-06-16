package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Transcript content must wrap to the viewport width, including
// unbreakable long tokens (URLs, hashes, paths) that word-wrap can't break
// on spaces. An overflowing line either corrupts the layout or gets clipped
// at draw — either way the user loses content. No rendered line may exceed
// the width it was rendered for.
func TestTimelineRenderNoOverflow(t *testing.T) {
	tl := newTimeline()
	tl.addUser("normal text then a " + strings.Repeat("x", 300) + " unbreakable token")
	tl.startTurn("t1", 0)
	tl.appendContent("t1", 0, "assistant reply with "+strings.Repeat("y", 250)+" inline")
	tl.endTurn("t1")

	for _, w := range []int{40, 80, 120} {
		for i, line := range tl.renderViewport(w, 200) {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width=%d: line %d overflows (%d cols > %d):\n%q", w, i, got, w, ansi.Strip(line))
			}
		}
	}
}
