package tui

import tea "charm.land/bubbletea/v2"

// handleMouse folds mouse events into transcript scrolling: the wheel scrolls
// the timeline line-by-line, and a left click on the right-edge scrollbar
// jumps the viewport to that position. Returns true when it consumed the
// event so Update can early-return. No-op (returns false) when a dialog or
// the expanded dashboard owns the screen, or for events it doesn't use.
func (m *UI) handleMouse(msg tea.Msg) bool {
	// Overlay panes own the screen: route the wheel to the active dialog when
	// it scrolls (clicks stay the dialog's own concern via keys).
	if m.overlay.active() {
		if e, ok := msg.(tea.MouseWheelMsg); ok {
			if s, ok := m.overlay.top().(scroller); ok {
				switch e.Mouse().Button {
				case tea.MouseWheelUp:
					s.scrollLines(-wheelStep)
					return true
				case tea.MouseWheelDown:
					s.scrollLines(wheelStep)
					return true
				}
			}
		}
		return false
	}
	if m.session.CockpitExpanded {
		return false
	}
	switch e := msg.(type) {
	case tea.MouseWheelMsg:
		mo := e.Mouse()
		r := m.layout.sidebar
		if !m.session.StateSidebarHidden && mo.X >= r.Min.X && mo.X < r.Max.X && mo.Y >= r.Min.Y && mo.Y < r.Max.Y {
			switch mo.Button {
			case tea.MouseWheelUp:
				m.scrollSidebar(-wheelStep)
				return true
			case tea.MouseWheelDown:
				m.scrollSidebar(wheelStep)
				return true
			}
		}
		switch e.Mouse().Button {
		case tea.MouseWheelUp:
			m.timeline.wheelUp() // line scroll, not item selection
			return true
		case tea.MouseWheelDown:
			m.timeline.wheelDown()
			return true
		}
		return false

	case tea.MouseClickMsg:
		mo := e.Mouse()
		if mo.Button != tea.MouseLeft {
			return false
		}
		main := m.layout.main
		g := transcriptGeometry(main)
		innerH := g.content.Dy()
		if innerH < 1 {
			return false
		}
		top := g.content.Min.Y
		if mo.Y < top || mo.Y >= top+innerH {
			return false
		}
		// The open transcript's scrollbar owns its final column. A click there
		// jumps to the matching fraction of the scroll range (top = top, bottom =
		// follow the tail).
		if mo.X == main.Max.X-1 {
			denom := max(innerH-1, 1)
			m.timeline.scrollToFraction(float64(mo.Y-top) / float64(denom))
			return true
		}
		// A click inside the content area toggles the [+]/[-] on that row, if it
		// bears one — the group / thinking header, or an individual tool / diff
		// sub-entry. Consume it only when something actually toggled, so a click
		// on plain text falls through.
		if mo.X >= g.surface.Min.X && mo.X < g.content.Min.X {
			return m.timeline.selectAtViewportLine(mo.Y - top)
		}
		if mo.X >= g.content.Min.X && mo.X < g.content.Max.X {
			return m.timeline.toggleAtViewportLine(mo.Y - top)
		}
	}
	return false
}
