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
		innerH := main.Dy() - 2
		if innerH < 1 {
			return false
		}
		top := main.Min.Y + 1
		if mo.Y < top || mo.Y >= top+innerH {
			return false
		}
		// The scrollbar is a 1-col gutter at main.Max.X-2: a click there jumps
		// to the matching fraction of the scroll range (top = top, bottom =
		// follow the tail).
		if mo.X == main.Max.X-2 {
			denom := max(innerH-1, 1)
			m.timeline.scrollToFraction(float64(mo.Y-top) / float64(denom))
			return true
		}
		// A click inside the content area toggles the [+]/[-] on that row, if it
		// bears one — the group / thinking header, or an individual tool / diff
		// sub-entry. Consume it only when something actually toggled, so a click
		// on plain text falls through.
		innerW := main.Dx() - 2 - scrollbarWidth
		if mo.X >= main.Min.X+1 && mo.X < main.Min.X+1+innerW {
			return m.timeline.toggleAtViewportLine(mo.Y - top)
		}
		return false
	}
	return false
}
