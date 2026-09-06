package tui

import tea "charm.land/bubbletea/v2"

// StartLogin starts the production OAuth setup while suppressing the external
// browser side effect. The returned command is owned by the caller.
func (h *OAuthBehaviorHarness) StartLogin(provider string) tea.Cmd {
	open := openBrowser
	openBrowser = func(string) tea.Cmd { return nil }
	defer func() { openBrowser = open }()
	return h.ui.startOAuthLogin(provider)
}
