package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Every character of a long, wrapping input must survive rendering. The
// composer wraps text to the inner width but then prepends a 2-column
// prompt prefix and pads back to the inner width — so wrapping to the
// full inner width clips the last two columns of each filled line.
func TestComposerWrapReservesPrefixWidth(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	c := composer{value: []rune(alphabet)} // cursor at 0

	// The mode prompt is wider than the old glyph-only prompt, so leave enough
	// height for every wrapped line.
	buf := uv.NewScreenBuffer(14, 10)
	c.draw(buf, buf.Bounds(), false)
	plain := ansi.Strip(buf.Render())

	var missing []rune
	for _, r := range alphabet {
		if !strings.ContainsRune(plain, r) {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("characters dropped by wrap/prefix clipping: %q\nrendered:\n%s", string(missing), plain)
	}
}
