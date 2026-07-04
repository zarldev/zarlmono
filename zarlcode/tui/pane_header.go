package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// headerPane is the quiet top bar: app identity + mode badge on the left,
// active model on the right. Read-only — no event handling.
type headerPane struct {
	session *Session
}

func newHeaderPane(session *Session) *headerPane {
	return &headerPane{session: session}
}

// Draw implements Pane.
func (h *headerPane) Draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dy() < 1 || area.Dx() < 4 {
		return
	}
	mode := h.session.headerMode()
	accent := palette.Assistant
	switch mode {
	case "build":
		accent = palette.Tool
	case "plan":
		accent = palette.PlanMode
	}
	left := strings.Join([]string{
		bracketed(palette.Primary.On(appDisplayName)),
		bracketed(accent.On(mode)),
	}, " ")

	var right string
	if h.session.Model != "" {
		right = bracketed(palette.Subtle.On(strings.ToLower(h.session.Model)))
	}

	// Lay out the left badges and the right-pinned model with an explicit
	// gap. lipgloss Align needs a width to align within — an Align with no
	// width is a no-op, which is what left the model jammed against the mode
	// badge instead of at the right edge. Widths are ANSI-aware (the badges
	// carry colour escapes); drawLine clips if the content can't fit.
	leftSeg := "  " + left
	rightSeg := right
	if rightSeg != "" {
		rightSeg += " "
	}
	gap := max(area.Dx()-lg.Width(leftSeg)-lg.Width(rightSeg), 1)
	bar := leftSeg + strings.Repeat(" ", gap) + rightSeg
	drawLine(scr, uv.Rect(area.Min.X, area.Min.Y, area.Dx(), 1), bar)
}

// Update implements Pane. Header is read-only.
func (h *headerPane) Update(msg tea.Msg) tea.Cmd { return nil }

// headerMode reports the current interaction mode for the header badge.
func (s *Session) headerMode() string {
	if s.PlanMode {
		return "plan"
	}
	if s.Run.Running {
		return "build"
	}
	return "chat"
}
