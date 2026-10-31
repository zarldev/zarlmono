package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// appearanceTickMsg schedules inherited preference reads on owned Tea commands.
type appearanceTickMsg struct{}

type appearancePollResult struct {
	choice    appearance.Choice
	err       error
	original  theme.Theme
	workspace string
}

func (m *UI) appearanceTick() tea.Cmd {
	if m.settings == nil || m.settings.Svc == nil {
		return nil
	}
	ctx := m.appContext()
	return func() tea.Msg {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return appearanceTickMsg{}
		}
	}
}

func (m *UI) appearanceRefreshCmd() tea.Cmd {
	if m.settings == nil || m.settings.Svc == nil || m.overlay.active() {
		return nil
	}
	ctx, svc, workspace, original := m.appContext(), m.settings.Svc, m.session.WorkspaceDir, palette
	return func() tea.Msg {
		store, err := appearanceStore()
		var choice appearance.Choice
		if err == nil {
			choice, err = themeChoice(ctx, store, svc, workspace)
		}
		return appearancePollResult{choice: choice, err: err, original: original, workspace: workspace}
	}
}

func (m *UI) receiveAppearance(v appearancePollResult) {
	if m.overlay.active() || palette != v.original || m.session.WorkspaceDir != v.workspace {
		return // A preview, selection or workspace transition superseded the read.
	}
	if v.err != nil {
		m.session.SetErrorToast("appearance: " + v.err.Error())
		return // Keep the current palette on malformed/inaccessible configuration.
	}
	if v.choice.Name != palette.Name {
		UseTheme(v.choice.Theme)
	}
}
