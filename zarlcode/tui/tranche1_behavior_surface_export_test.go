package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// DashboardRoutingHarness exposes dashboard key routing to external behavior tests.
type DashboardRoutingHarness struct {
	ui *UI
}

// NewDashboardRoutingHarness creates a UI with the dashboard expanded.
func NewDashboardRoutingHarness() *DashboardRoutingHarness {
	ui := New()
	ui.session.SetCockpitExpanded(true)
	return &DashboardRoutingHarness{ui: ui}
}

// Press routes a key through the full production UI update path.
func (h *DashboardRoutingHarness) Press(msg tea.KeyPressMsg) { _, _ = h.ui.Update(msg) }

// Tab returns the active dashboard tab name.
func (h *DashboardRoutingHarness) Tab() string {
	return contextViewTabNames[h.ui.contextView.tab]
}

// PlanMode reports the current composer mode.
func (h *DashboardRoutingHarness) PlanMode() bool { return h.ui.session.PlanMode }

// Collapse closes the dashboard without changing other UI state.
func (h *DashboardRoutingHarness) Collapse() { h.ui.session.SetCockpitExpanded(false) }

// SettingsPromotionHarness exposes settings key routing to external behavior tests.
type SettingsPromotionHarness struct {
	ui     *UI
	dialog *settingsDialog
}

// NewSettingsPromotionHarness opens settings and focuses the row identified by key.
func NewSettingsPromotionHarness(ctx context.Context, settings *engine.Settings, key string) *SettingsPromotionHarness {
	dialog := newSettingsDialog(ctx, settings)
	ui := New()
	ui.SetSettings(settings)
	ui.overlay.push(dialog)
	harness := &SettingsPromotionHarness{ui: ui, dialog: dialog}
	for cat := range dialog.cats {
		for row := range dialog.cats[cat].rows {
			if dialog.cats[cat].rows[row].key == key {
				dialog.cat = cat
				dialog.row = row
				dialog.focusRows = true
				return harness
			}
		}
	}
	return harness
}

// Press routes a key through the full production UI update path.
func (h *SettingsPromotionHarness) Press(msg tea.KeyPressMsg) tea.Cmd {
	_, cmd := h.ui.Update(msg)
	return cmd
}

// HelpOpen reports whether contextual help is topmost.
func (h *SettingsPromotionHarness) HelpOpen() bool {
	if !h.ui.overlay.active() {
		return false
	}
	_, ok := h.ui.overlay.top().(*helpDialog)
	return ok
}

// HelpText returns the visible shortcut text from the topmost help dialog.
func (h *SettingsPromotionHarness) HelpText() string {
	if !h.ui.overlay.active() {
		return ""
	}
	d, ok := h.ui.overlay.top().(*helpDialog)
	if !ok {
		return ""
	}
	return strings.Join(d.lines(), "\n")
}

// SettingsTopmost reports whether closing help returned to the same settings dialog.
func (h *SettingsPromotionHarness) SettingsTopmost() bool {
	return h.ui.overlay.active() && h.ui.overlay.top() == h.dialog
}

// Focus returns the settings selection state so routing tests can detect mutation.
func (h *SettingsPromotionHarness) Focus() (int, int, bool) {
	return h.dialog.cat, h.dialog.row, h.dialog.focusRows
}
