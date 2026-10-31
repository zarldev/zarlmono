package tui

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// drawSidebar paints the single state sidebar into r.
func (m *UI) drawSidebar(scr uv.Screen, r uv.Rectangle) {
	// Keep the conversation and sidebar separate without nested frames. Live
	// activity and elapsed time belong to the composer, not another sidebar clock.
	w, h := r.Dx(), r.Dy()
	if w < 6 || h < 4 {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		drawLine(scr, uv.Rect(r.Min.X, y, 1, 1), palette.Border.On("│"))
	}
	x, innerW := r.Min.X+2, w-3
	drawLine(scr, uv.Rect(x, r.Min.Y, innerW, 1), sidebarHeading("session"))
	lines := m.stateSidebarLines(innerW, h-3)
	m.sidebarScroll = min(max(0, m.sidebarScroll), max(0, len(lines)-(h-3)))
	end := min(len(lines), m.sidebarScroll+h-3)
	for i, line := range lines[m.sidebarScroll:end] {
		drawLine(scr, uv.Rect(x, r.Min.Y+1+i, innerW, 1), ansi.Truncate(line, innerW, "…"))
	}
	if len(lines) > h-3 {
		hint := itoa(m.sidebarScroll+1) + "–" + itoa(end) + "/" + itoa(len(lines)) + " · alt+↑↓ scroll"
		drawLine(scr, uv.Rect(x, r.Max.Y-2, innerW, 1), palette.Subtle.On(hint))
	}
	// Keep details reachable even when the sidebar body overflows.
	drawLine(scr, uv.Rect(x, r.Max.Y-1, innerW, 1), palette.Subtle.On("ctrl+l more · ctrl+p plan"))
}

func (m *UI) stateSidebarLines(innerW, innerH int) []string {
	return m.stateSidebarContent(innerW, innerH)
}

// scrollSidebar keeps sidebar navigation separate from transcript browsing.
func (m *UI) scrollSidebar(delta int) {
	r := m.layout.sidebar
	if r.Dx() < 6 || r.Dy() < 4 {
		return
	}
	lines := m.stateSidebarLines(r.Dx()-3, r.Dy()-3)
	maximum := max(0, len(lines)-(r.Dy()-3))
	m.sidebarScroll = min(max(0, m.sidebarScroll), maximum)
	m.sidebarScroll = min(max(0, m.sidebarScroll+delta), maximum)
}

func (m *UI) handleSidebarKey(key string) bool {
	r := m.layout.sidebar
	if m.session.StateSidebarHidden || r.Dx() < 6 || r.Dy() < 4 {
		return false
	}
	switch key {
	case "alt+up":
		m.scrollSidebar(-1)
	case "alt+down":
		m.scrollSidebar(1)
	case "alt+pgup":
		m.scrollSidebar(-(r.Dy() - 3))
	case "alt+pgdown":
		m.scrollSidebar(r.Dy() - 3)
	case "alt+home":
		m.sidebarScroll = 0
	case "alt+end":
		m.sidebarScroll = len(m.stateSidebarLines(r.Dx()-3, r.Dy()-3))
		m.scrollSidebar(0)
	default:
		return false
	}
	return true
}
