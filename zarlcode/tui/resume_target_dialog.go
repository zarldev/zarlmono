package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type actionResumeSession struct {
	session  *savedSession
	useSaved bool
}

func (actionResumeSession) isAction() {}

type resumeTargetDialog struct {
	saved   *savedSession
	current string
}

func newResumeTargetDialog(saved *savedSession, currentProvider, currentModel string) *resumeTargetDialog {
	return &resumeTargetDialog{
		saved:   saved,
		current: providerModelLabel(currentProvider, currentModel),
	}
}

func (d *resumeTargetDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "s", "S", "enter":
		return actionResumeSession{session: d.saved, useSaved: true}
	case "c", "C":
		return actionResumeSession{session: d.saved, useSaved: false}
	}
	return actionClose{}
}

func (d *resumeTargetDialog) draw(scr uv.Screen, area uv.Rectangle) {
	saved := "unknown"
	if d != nil && d.saved != nil {
		saved = providerModelLabel(d.saved.Provider, d.saved.Model)
	}
	drawActionDialog(scr, area, "resume", "choose model target", []string{
		palette.Warning.On("saved session uses a different model target"),
		palette.Subtle.On("saved") + palette.Muted.On("    ") + saved,
		palette.Subtle.On("current") + palette.Muted.On("  ") + d.current,
	}, keyLegend(keyHint{"s / enter", "use saved"}, keyHint{"c", "use current"}, keyHint{"any other", "cancel"}), 76)
}

func providerModelLabel(provider, model string) string {
	if provider == "" && model == "" {
		return "unknown"
	}
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	return provider + " / " + model
}
