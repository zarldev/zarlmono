package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// OAuthBehaviorHarness drives OAuth ownership and result routing through the
// production settings/providers action path for black-box tests.
type OAuthBehaviorHarness struct {
	ui        *UI
	settings  *settingsDialog
	providers *providersDialog
}

// NewOAuthBehaviorHarness opens the production settings/providers surface.
func NewOAuthBehaviorHarness(ctx context.Context, settings *engine.Settings) *OAuthBehaviorHarness {
	ui := New()
	ui.SetSettings(settings)
	dialog := newSettingsDialog(ctx, settings)
	ui.overlay.push(dialog)
	return &OAuthBehaviorHarness{ui: ui, settings: dialog, providers: dialog.providers}
}

// StartAttempt installs one cancellable callback attempt, superseding any
// attempt already owned by the surface.
func (h *OAuthBehaviorHarness) StartAttempt(provider string, cancel context.CancelFunc) uint64 {
	id := h.ui.beginOAuthOperation(provider, h.providers, cancel)
	h.providers.beginOAuth("https://oauth.example/authorize")
	return id
}

// PressSettingsEscape routes Esc through the embedded providers panel and root action owner.
func (h *OAuthBehaviorHarness) PressSettingsEscape() {
	action := h.settings.handleProviders(tea.KeyPressMsg{Code: tea.KeyEsc})
	h.ui.handleAction(action)
}

// CloseSettings closes the owning settings surface through the root action path.
func (h *OAuthBehaviorHarness) CloseSettings() { h.ui.handleAction(actionClose{}) }

// ApplySuccess routes an identified OAuth completion through the production stale-result gate.
func (h *OAuthBehaviorHarness) ApplySuccess(id uint64, provider, account string) {
	h.ui.handleOAuthMsg(oauthDoneMsg{id: id, provider: provider, account: account})
}

// ApplyFailure routes an identified OAuth failure through the production stale-result gate.
func (h *OAuthBehaviorHarness) ApplyFailure(id uint64, provider string, err error) {
	h.ui.handleOAuthMsg(oauthFailedMsg{id: id, provider: provider, err: err})
}

// Status returns the providers panel's latest user-visible status.
func (h *OAuthBehaviorHarness) Status() string { return h.providers.status }

// Busy reports whether the providers panel is waiting on a cancellable callback.
func (h *OAuthBehaviorHarness) Busy() bool { return h.providers.oauthBusy }

// InSubMode reports whether settings delegates Esc to the providers panel.
func (h *OAuthBehaviorHarness) InSubMode() bool { return h.providers.inSubMode() }

// Footer returns the providers panel's current key legend.
func (h *OAuthBehaviorHarness) Footer() string { return h.providers.footerHint() }

// ActiveAttempt returns the currently owned operation ID, or zero when none is active.
func (h *OAuthBehaviorHarness) ActiveAttempt() uint64 {
	if h.ui.oauthOperation == nil {
		return 0
	}
	return h.ui.oauthOperation.id
}

// SetConfirmQuit controls the production root confirmation policy.
func (h *OAuthBehaviorHarness) SetConfirmQuit(confirm bool) {
	h.ui.session.SetConfirmQuit(confirm)
}

// UpdateKey routes a key through the full production UI update path.
func (h *OAuthBehaviorHarness) UpdateKey(msg tea.KeyPressMsg) tea.Cmd {
	_, cmd := h.ui.Update(msg)
	return cmd
}

// ApplySuccessThroughUpdate routes a late completion through the full production UI update path.
func (h *OAuthBehaviorHarness) ApplySuccessThroughUpdate(id uint64, provider, account string) {
	_, _ = h.ui.Update(oauthDoneMsg{id: id, provider: provider, account: account})
}

// QuitConfirmationOpen reports whether root quit policy pushed its confirmation modal.
func (h *OAuthBehaviorHarness) QuitConfirmationOpen() bool {
	if !h.ui.overlay.active() {
		return false
	}
	_, ok := h.ui.overlay.top().(*quitConfirmDialog)
	return ok
}
