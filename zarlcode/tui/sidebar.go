package tui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// drawSidebar paints the single state sidebar into r.
func (m *UI) drawSidebar(scr uv.Screen, r uv.Rectangle) {
	// The sidebar is secondary context, separated from the transcript by one
	// vertical rule instead of a complete box. Run state already lives in the
	// transcript utility header.
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, 1, r.Dy()), palette.Border.On(strings.Repeat("│", r.Dy())))
	drawLine(scr, uv.Rect(r.Min.X+1, r.Min.Y, max(r.Dx()-1, 0), 1), palette.Primary.On(" context"))
	drawLine(scr, uv.Rect(r.Min.X+1, r.Min.Y+1, max(r.Dx()-1, 0), 1), palette.Border.On(strings.Repeat("─", max(r.Dx()-1, 0))))

	w, h := r.Dx(), r.Dy()
	if w < 6 || h < 4 {
		return
	}
	// sidePad is a one-column gutter inside the border on each side, so
	// content doesn't butt against the frame. Section rules override this
	// below so they read as dividers in the frame itself.
	const sidePad = 1
	innerW := w - 1 - 2*sidePad
	innerH := h - 2

	lines := m.stateSidebarLines(innerW, innerH)

	for i, ln := range lines {
		if i >= innerH {
			break
		}
		// Section rules are emitted with ANSI styling, so detect them after
		// stripping escapes. They span the full pane width to avoid the normal
		// one-column content gutters rendering as "│ ├─... │".
		if strings.HasPrefix(ansi.Strip(ln), "├") {
			drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+2+i, w, 1), paneSectionRule(ln, w))
		} else {
			drawLine(scr, uv.Rect(r.Min.X+1+sidePad, r.Min.Y+2+i, innerW, 1), ln)
		}
	}
}

func paneSectionRule(line string, width int) string {
	if width < 1 {
		return ""
	}
	lineW := ansi.StringWidth(line)
	if lineW >= width {
		return ansi.Truncate(line, width, "")
	}
	fill := max(width-lineW-1, 0)
	return line + palette.Border.On(strings.Repeat("─", fill)+"┤")
}

// drawSectionRule paints a section-head line as a full-width rule joining the
// pane's left separator (r.Min.X-1) and right border (r.Max.X).
func drawSectionRule(scr uv.Screen, r uv.Rectangle, y int, line string) {
	width := r.Dx() + 2
	drawLine(scr, uv.Rect(r.Min.X-1, y, width, 1), paneSectionRule(line, width))
}

func (m *UI) stateSidebarLines(innerW, innerH int) []string {
	return m.stateSidebarContent(innerW, innerH)
}
