package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// modelsLoadedMsg carries an async model-list fetch back to the settings
// overlay (see actionFetchModels / settingsDialog.onModelsLoaded).
type modelsLoadedMsg struct {
	provider string
	models   []string
	err      error
}

// settingsTickMsg is the settings surface's heartbeat: a slow tick that keeps
// the view repainting while the overlay is open so a footer status toast ages
// out on its own (the cockpit frame tick only runs during a live turn).
type settingsTickMsg struct{}

const settingsTickInterval = time.Second

func settingsTick() tea.Cmd {
	return oneShotTimerCmd(settingsTickInterval, func(time.Time) tea.Msg { return settingsTickMsg{} })
}

// openSettings pushes the settings overlay and kicks an initial model fetch
// for the active provider, so the model picker is populated by the time the
// user navigates to it.
func (m *UI) openSettings() tea.Cmd {
	if m.settings == nil {
		return nil
	}
	m.captureSettingsTargetSnapshot()
	d := newSettingsDialog(m.appContext(), m.settings)
	// Append rather than reorder existing routing/editor rows.
	if m.live != nil {
		for i := range d.cats {
			if d.cats[i].name == "model" {
				d.cats[i].rows = append(d.cats[i].rows, settingsRow{
					label: "gateway advertised", kind: rowAction,
					desc: "Cached declarations for the actual active connection. Optional refresh; not capabilities or billing authority.",
					open: func(*engine.Settings) dialog { return newTargetModelInfoDialog(m.live.RunTarget()) },
				})
			}
		}
	}
	d.gallery.workspace = m.session.WorkspaceDir
	d.gallery.refresh()
	m.overlay.push(d)
	cmds := []tea.Cmd{settingsTick()} // toast heartbeat
	if p := d.currentProvider(); p != "" {
		d.modelsLoading[p] = true
		cmds = append(cmds, m.fetchModelsCmd(p))
	}
	// Prefetch the compaction provider's models too when it's a different
	// backend, so its model picker is populated when reached.
	if cp := d.compactProvider(); cp != "" && cp != d.currentProvider() {
		d.modelsLoading[cp] = true
		cmds = append(cmds, m.fetchModelsCmd(cp))
	}
	return tea.Batch(cmds...)
}

// fetchModelsCmd probes the provider's model list off the Update loop
// (bounded), reporting the result as a modelsLoadedMsg. The registry
// resolves the key/endpoint and falls back to seed models on any failure.
func (m *UI) fetchModelsCmd(provider string) tea.Cmd {
	if d, ok := m.overlay.top().(*targetModelInfoDialog); ok {
		if m.live == nil || !d.matches(m.live.RunTarget()) {
			d.targetChanged = true
			return nil
		}
		return m.RefreshTargetModelInfo()
	}
	// Bind summaries only to the actual construction, never a settings-global
	// map keyed by mutable provider names or guessed credential identities.
	if m.live != nil {
		if p, ok := m.overlay.top().(*modelQuickPick); ok {
			p.meta.target = m.live.RunTarget
		}
	}
	if m.settings == nil || m.settings.Registry == nil || provider == "" {
		return nil
	}
	reg := m.settings.Registry
	parent := m.appContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, providerModelFetchTimeout)
		defer cancel()
		models, err := reg.FetchModels(ctx, provider)
		return modelsLoadedMsg{provider: provider, models: models, err: err}
	}
}

// handleModelsMsg routes a completed fetch to the open settings dialog, or to
// a modelQuickPick overlay if one is active. Also populates the model cache so
// subsequent ctrl+e opens are instant.
func (m *UI) handleModelsMsg(msg tea.Msg) bool {
	if m.handleTargetModelInfoMsg(msg) {
		return true
	}
	lm, ok := msg.(modelsLoadedMsg)
	if !ok {
		return false
	}
	m.session.CacheModels(lm.provider, lm.models)
	// Route to settings dialog if open.
	if d, ok := topSettingsDialog(m); ok {
		d.onModelsLoaded(lm.provider, lm.models, lm.err)
		return true
	}
	// Route to model quick pick if it's on the overlay.
	for i := len(m.overlay.stack) - 1; i >= 0; i-- {
		if mp, ok := m.overlay.stack[i].(*modelQuickPick); ok {
			mp.setModels(lm.provider, lm.models)
			return true
		}
	}
	return true
}

func topSettingsDialog(m *UI) (*settingsDialog, bool) {
	for i := len(m.overlay.stack) - 1; i >= 0; i-- {
		if d, ok := m.overlay.stack[i].(*settingsDialog); ok {
			return d, true
		}
	}
	return nil, false
}
