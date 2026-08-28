package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// mainToastTTL is how long a status-bar notification lingers.
const mainToastTTL = 4 * time.Second

// mainToastMsg wakes the Update loop so an idle status-bar toast can clear.
type mainToastMsg struct{}

// statusPane is the contextual bottom state row. It stays quiet when there is
// nothing requiring attention; complete shortcut discovery belongs in help.
type statusPane struct {
	session *Session
	input   func() string
}

func newStatusPane(session *Session, input func() string) *statusPane {
	return &statusPane{session: session, input: input}
}

// Draw implements Pane.
func (s *statusPane) Draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dy() < 1 || area.Dx() < 4 {
		return
	}
	hint := s.statusHint()
	toast := s.statusToast()
	slashActive := s.input != nil && slashStatusHint(s.input()) != ""
	if slashActive && s.session.ToastTone != toastError && s.session.ToastTone != toastWarn {
		toast = ""
	}

	content := uv.Rect(area.Min.X+1, area.Min.Y, max(area.Dx()-1, 0), 1)
	if content.Dx() == 0 {
		return
	}
	if toast == "" {
		drawLine(scr, content, hint)
		return
	}

	// Keep both durable context and transient feedback when they fit. On a
	// collision the toast owns the row: exceptional/transient feedback is more
	// actionable than the durable mode label, and explicit arbitration avoids
	// the old right-aligned overlay corrupting the left segment.
	toastWidth := ansi.StringWidth(toast)
	hintWidth := ansi.StringWidth(hint)
	if hintWidth+2+toastWidth <= content.Dx() {
		drawLine(scr, content, hint)
		x := content.Max.X - toastWidth
		drawLine(scr, uv.Rect(x, content.Min.Y, toastWidth, 1), toast)
		return
	}
	drawLine(scr, content, toast)
}

// Update implements Pane. Status bar is read-only.
func (s *statusPane) Update(msg tea.Msg) tea.Cmd { return nil }

// toastExpiryCmd schedules a wake-up to clear an active toast.

func (s *statusPane) statusHint() string {
	if s.input != nil {
		if hint := slashStatusHint(s.input()); hint != "" {
			return hint
		}
	}
	mode := palette.Primary.On("build mode")
	if s.session.PlanMode {
		mode = palette.PlanMode.On("plan mode")
	}
	return mode
}

func (s *statusPane) statusToast() string {
	if s.session.Toast == "" || time.Since(s.session.ToastAt) > mainToastTTL {
		return ""
	}
	return renderFooterToast(s.session.Toast, s.session.ToastTone)
}
