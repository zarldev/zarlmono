package tui

import (
	"strings"

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
	lines := []string{
		overlayTopBar("resume", nil, 0, "target", 72),
		palette.Subtle.On(strings.Repeat("─", 72)),
		palette.Warning.On("saved session uses a different model target"),
		"",
		palette.Subtle.On("saved") + palette.Muted.On("    ") + saved,
		palette.Subtle.On("current") + palette.Muted.On("  ") + d.current,
		"",
		palette.Subtle.On("s / enter") + palette.Muted.On("  resume with saved target"),
		palette.Subtle.On("c") + palette.Muted.On("          resume with current target"),
		palette.Subtle.On("any other key") + palette.Muted.On("  cancel"),
	}
	drawDialogBox(scr, area, "resume", lines)
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
