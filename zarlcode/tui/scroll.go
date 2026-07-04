package tui

import uv "github.com/charmbracelet/ultraviolet"

// Shared vertical-scroll furniture so overlay panes scroll exactly like the
// transcript: a right-edge scrollbar gutter (same geometry + glyphs) and a
// mouse-wheel seam. Keyboard paging stays each pane's own concern.

// wheelStep is how many lines one wheel notch scrolls in an overlay pane.
const wheelStep = 3

// scroller is implemented by overlay dialogs that scroll vertically. The mouse
// wheel routes to the active dialog through it; n is signed lines (negative =
// up). Implementations adjust their own offset and let draw clamp it.
type scroller interface{ scrollLines(n int) }

// paneScrollbarGeom mirrors the transcript's thumb geometry for a content of
// `total` lines shown in a `height`-row gutter scrolled to `off`, so every
// pane's scrollbar looks identical. Inactive (all-track) when it all fits.
func paneScrollbarGeom(total, height, off int) scrollbarGeom {
	if height <= 0 || total <= height {
		return scrollbarGeom{Height: height}
	}
	thumbH := max(height*height/total, 1)
	maxOff := total - height
	if off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	thumbStart := 0
	if height > thumbH {
		thumbStart = (height - thumbH) * off / maxOff
	}
	return scrollbarGeom{
		Active:     true,
		Height:     height,
		ThumbStart: thumbStart,
		ThumbEnd:   thumbStart + thumbH - 1,
	}
}

// drawPaneScrollbar paints a 1-col scrollbar at column x, from row `top` down
// `height` rows, for content of `total` lines scrolled to `off`. Matches the
// transcript gutter: Border track, Primary thumb.
func drawPaneScrollbar(scr uv.Screen, x, top, height, total, off int) {
	if height <= 0 {
		return
	}
	g := paneScrollbarGeom(total, height, off)
	track := palette.Border.On("│")
	thumb := palette.Primary.On("█")
	for i := range height {
		glyph := track
		if g.Active && i >= g.ThumbStart && i <= g.ThumbEnd {
			glyph = thumb
		}
		drawLine(scr, uv.Rect(x, top+i, 1, 1), glyph)
	}
}

// clampScrollOffset keeps off within [0, max(0, total-height)].
func clampScrollOffset(off, total, height int) int {
	maxOff := max(total-height, 0)
	if off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	return off
}
