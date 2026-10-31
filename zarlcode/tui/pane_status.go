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
	session    *Session
	input      func() string
	settlement func() settlementNotice
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
	settling := s.settlement != nil && s.settlement().text != ""
	if slashActive && !settling && s.session.ToastTone != toastError && s.session.ToastTone != toastWarn {
		toast = ""
	}

	content := uv.Rect(area.Min.X+2, area.Min.Y, max(area.Dx()-3, 0), 1)
	if content.Dx() == 0 {
		return
	}
	message := toast
	if message == "" {
		message = hint
	} else if hint != "" && ansi.StringWidth(message)+2+ansi.StringWidth(hint) <= content.Dx() {
		message += "  " + hint
	}
	drawLine(scr, content, terminalChrome().Status(message, "", content.Dx()))
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
	return ""
}

func (s *statusPane) statusToast() string {
	if s.settlement != nil {
		if status := s.settlement(); status.text != "" {
			return renderFooterToast(status.text, status.tone)
		}
	}
	if s.session.Toast == "" || time.Since(s.session.ToastAt) > mainToastTTL {
		return ""
	}
	return renderFooterToast(s.session.Toast, s.session.ToastTone)
}
