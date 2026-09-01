package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

const maxSessionLabelRunes = 80

type sessionNameDialog struct{ input composer }

func newSessionNameDialog(label string) *sessionNameDialog {
	d := &sessionNameDialog{}
	d.input.insert(label)
	return d
}

func (d *sessionNameDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+n":
		return actionClose{}
	case "enter":
		return actionSetSessionLabel{label: normalizeSessionLabel(d.input.text())}
	case "backspace":
		d.input.backspace()
	case "left":
		d.input.left()
	case "right":
		d.input.right()
	default:
		if msg.Text != "" && len([]rune(d.input.text())) < maxSessionLabelRunes {
			d.input.insert(msg.Text)
		}
	}
	return actionNone{}
}

func (d *sessionNameDialog) handlePaste(text string) {
	remaining := maxSessionLabelRunes - len([]rune(d.input.text()))
	if remaining <= 0 {
		return
	}
	runes := []rune(strings.ReplaceAll(text, "\n", " "))
	if len(runes) > remaining {
		runes = runes[:remaining]
	}
	d.input.insert(string(runes))
}

func (d *sessionNameDialog) draw(scr uv.Screen, area uv.Rectangle) {
	label := d.input.text()
	if label == "" {
		label = palette.Subtle.On("(unnamed)")
	} else {
		label += palette.Primary.On("▏")
	}
	drawActionDialog(scr, area, "session name", "shown in resume", []string{
		palette.Muted.On("Give this session a memorable label."),
		label,
	}, keyLegend(keyHint{"enter", "save"}, keyHint{"esc", "cancel"}), 76)
}

func normalizeSessionLabel(label string) string {
	label = strings.Join(strings.Fields(label), " ")
	runes := []rune(label)
	if len(runes) > maxSessionLabelRunes {
		label = string(runes[:maxSessionLabelRunes])
	}
	return label
}

func (m *UI) openSessionNameDialog() {
	m.overlay.push(newSessionNameDialog(m.session.Label))
}

func (m *UI) setSessionLabel(label string) tea.Cmd {
	m.session.Label = normalizeSessionLabel(label)
	m.session.LabelManual = true
	if m.session.Label == "" {
		m.session.SetToast("session name cleared")
	} else {
		m.session.SetToast("session named " + m.session.Label)
	}
	return tea.Batch(m.toastExpiryCmd(), m.persistSessionLabelCmd())
}

func (m *UI) persistSessionLabelCmd() tea.Cmd {
	settings := m.settings
	id, label := m.session.ID, m.session.Label
	if id == "" || settings == nil || settings.Store == nil {
		return nil
	}
	baseCtx := context.WithoutCancel(m.appContext())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(baseCtx, sessionSaveCommandTTL)
		defer cancel()
		if err := settings.Store.RenameSession(ctx, id, label); err != nil {
			return sessionSaveFailedMsg{Error: err.Error()}
		}
		return nil
	}
}
