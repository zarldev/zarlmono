package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// mainToastTTL is how long a status-bar notification lingers.
const mainToastTTL = 4 * time.Second

// mainToastMsg wakes the Update loop so an idle status-bar toast can clear.
type mainToastMsg struct{}

// statusPane is the bottom hint bar: key hints on the left, transient toast
// on the right. Read-only — no keyboard event handling.
type statusPane struct {
	session *Session
}

func newStatusPane(session *Session) *statusPane {
	return &statusPane{session: session}
}

// Draw implements Pane.
func (s *statusPane) Draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dy() < 1 || area.Dx() < 4 {
		return
	}
	hint := s.statusHint()
	bar := lg.NewStyle().
		Background(lgColor(palette.Highlight)).
		Foreground(lgColor(palette.Subtle)).
		Width(area.Dx()).
		Render(hint)
	drawLine(scr, area, bar)

	// Toast overlay at the right edge. Right-align it when it fits; when it's
	// wider than the bar, pin it to the left and let drawLine truncate —
	// a clipped notification beats a silently dropped one.
	toast := s.statusToast()
	if toast != "" {
		tw := ansi.StringWidth(toast)
		x := area.Min.X + area.Dx() - tw
		if x < area.Min.X {
			x = area.Min.X
		}
		drawLine(scr, uv.Rect(x, area.Min.Y, area.Min.X+area.Dx()-x, 1), toast)
	}
}

// Update implements Pane. Status bar is read-only.
func (s *statusPane) Update(msg tea.Msg) tea.Cmd { return nil }

// toastExpiryCmd schedules a wake-up to clear an active toast.

func (s *statusPane) statusHint() string {
	stopKey := "ctrl+c quit  ·  ctrl+q clear"
	if s.session.Run.Running {
		stopKey = "esc stop  ·  ctrl+c quit  ·  ctrl+q clear"
	}
	if s.session.CockpitExpanded {
		return " ↑↓/jk scroll  ·  pgup/pgdn page  ·  home/end jump  ·  ctrl+l / esc / q close  ·  " + stopKey + "  ·  ctrl+g keys"
	}
	// These still reference m.timeline and m.composer — will be refactored
	// when those become proper panes.
	if s.session.PlanMode {
		return " enter submit  ·  shift+enter newline  ·  shift+tab build  ·  " + stopKey + "  ·  ctrl+g keys"
	}
	return " enter submit  ·  shift+enter newline  ·  tab browse  ·  shift+tab plan  ·  " + stopKey + "  ·  ctrl+g keys"
}

func (s *statusPane) statusToast() string {
	if s.session.Toast == "" || time.Since(s.session.ToastAt) > mainToastTTL {
		return ""
	}
	return renderFooterToast(s.session.Toast, s.session.ToastTone)
}
