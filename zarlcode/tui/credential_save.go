package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

type credentialSaveRequest struct {
	provider  string
	value     string
	mcpServer *db.MCPServerRow
	done      func(error)
}

func (r credentialSaveRequest) finish(err error) {
	if r.done != nil {
		r.done(err)
	}
}

func (r credentialSaveRequest) save(ctx context.Context, settings *engine.Settings) error {
	if r.mcpServer != nil {
		return settings.Svc.SetMCPServer(ctx, *r.mcpServer, r.value)
	}
	return settings.Svc.SetKey(ctx, prefs.ScopeGlobal, r.provider, r.value)
}

func (r credentialSaveRequest) setup(ctx context.Context, settings *engine.Settings, passphrase string) error {
	if r.mcpServer != nil {
		return settings.SetupCredentialVaultAndSetMCPServer(ctx, passphrase, *r.mcpServer, r.value)
	}
	return settings.SetupCredentialVaultAndSetKey(ctx, passphrase, prefs.ScopeGlobal, r.provider, r.value)
}

type actionSaveCredential struct{ request credentialSaveRequest }

func (actionSaveCredential) isAction() {}

type actionEnsureCredentialVault struct {
	next   action
	cancel func()
}

func (actionEnsureCredentialVault) isAction() {}

type actionSubmitCredentialVault struct {
	dialog     *credentialVaultDialog
	passphrase string
}

func (actionSubmitCredentialVault) isAction() {}

type credentialVaultResultMsg struct {
	dialog *credentialVaultDialog
	err    error
}

type credentialVaultDialog struct {
	model    *vaultUnlockModel
	settings *engine.Settings
	save     *credentialSaveRequest
	next     action
	cancel   func()
	busy     bool
}

func newCredentialVaultDialog(settings *engine.Settings, setup bool) *credentialVaultDialog {
	return &credentialVaultDialog{model: newVaultUnlockModel(setup, false), settings: settings}
}

func (d *credentialVaultDialog) fullScreen() bool { return true }

func (d *credentialVaultDialog) handlePaste(content string) {
	if !d.busy {
		d.model.insert(content)
	}
}

func (d *credentialVaultDialog) handleKey(msg tea.KeyPressMsg) action {
	if d.busy {
		return actionNone{}
	}
	if msg.String() == "esc" || msg.String() == "ctrl+c" {
		if d.save != nil && d.save.done != nil {
			d.save.done(errVaultUnlockCancelled)
		}
		if d.cancel != nil {
			d.cancel()
		}
		d.clear()
		return actionClose{}
	}
	_ = d.model.handleKey(msg)
	if !d.model.done {
		return actionNone{}
	}
	passphrase := d.model.out
	d.model.out = ""
	d.model.pass = nil
	d.model.confirm = nil
	d.model.done = false
	d.busy = true
	return actionSubmitCredentialVault{dialog: d, passphrase: passphrase}
}

func (d *credentialVaultDialog) draw(scr uv.Screen, area uv.Rectangle) {
	d.model.width, d.model.height = area.Dx(), area.Dy()
	if d.busy {
		d.model.err = "setting up credential protection…"
	}
	drawSplash(scr, area, palette.Primary, d.model.infoLines())
}

func (d *credentialVaultDialog) clear() {
	d.model.out = ""
	d.model.pass = nil
	d.model.confirm = nil
	if d.save != nil {
		d.save.value = ""
		d.save.mcpServer = nil
	}
}

func (m *UI) handleCredentialAction(a action) (tea.Cmd, bool) {
	switch a := a.(type) {
	case actionSaveCredential:
		if m.settings == nil || m.settings.Svc == nil {
			a.request.finish(errors.New("credential service unavailable"))
			return nil, true
		}
		mode, err := m.settings.Svc.CredentialProtection(m.appContext())
		if err != nil {
			a.request.finish(err)
			return nil, true
		}
		if mode == prefs.CredentialProtectionOff || m.settings.Svc.HasVault() {
			err = a.request.save(m.appContext(), m.settings)
			a.request.finish(err)
			return nil, true
		}
		d, err := m.credentialVaultDialog()
		if err != nil {
			a.request.finish(err)
			return nil, true
		}
		d.save = &a.request
		m.overlay.push(d)
		return nil, true
	case actionEnsureCredentialVault:
		if m.settings == nil || m.settings.Svc == nil {
			if a.cancel != nil {
				a.cancel()
			}
			return nil, true
		}
		mode, err := m.settings.Svc.CredentialProtection(m.appContext())
		if err != nil || mode == prefs.CredentialProtectionOff || m.settings.Svc.HasVault() {
			if err != nil {
				if a.cancel != nil {
					a.cancel()
				}
				return nil, true
			}
			return m.handleAction(a.next), true
		}
		d, err := m.credentialVaultDialog()
		if err != nil {
			if a.cancel != nil {
				a.cancel()
			}
			return nil, true
		}
		d.next, d.cancel = a.next, a.cancel
		m.overlay.push(d)
		return nil, true
	case actionSubmitCredentialVault:
		d := a.dialog
		passphrase := a.passphrase
		return func() tea.Msg {
			defer func() { passphrase = "" }()
			ctx := m.appContext()
			var err error
			if d.save != nil {
				err = d.save.setup(ctx, d.settings, passphrase)
			} else {
				err = d.settings.SetupCredentialVault(ctx, passphrase)
			}
			return credentialVaultResultMsg{dialog: d, err: err}
		}, true
	}
	return nil, false
}

func (m *UI) credentialVaultDialog() (*credentialVaultDialog, error) {
	exists, err := m.settings.CredentialVaultExists()
	if err != nil {
		return nil, err
	}
	return newCredentialVaultDialog(m.settings, !exists), nil
}

func (m *UI) handleCredentialVaultResult(msg credentialVaultResultMsg) tea.Cmd {
	d := msg.dialog
	if !m.overlay.active() || m.overlay.top() != d {
		return nil
	}
	if errors.Is(msg.err, vault.ErrWrongPassphrase) {
		d.model = newVaultUnlockModel(false, true)
		d.busy = false
		return nil
	}
	m.overlay.pop()
	defer d.clear()
	if d.save != nil {
		if d.save.done != nil {
			d.save.done(msg.err)
		}
		return nil
	}
	if msg.err != nil {
		if d.cancel != nil {
			d.cancel()
		}
		return nil
	}
	return m.handleAction(d.next)
}

// SaveCredential starts the same protected credential flow used by all TUI
// credential entry points. It is also a narrow integration seam for embedders.
func (m *UI) SaveCredential(provider, value string, done func(error)) tea.Cmd {
	return m.handleAction(actionSaveCredential{request: credentialSaveRequest{provider: provider, value: value, done: done}})
}

var _ dialog = (*credentialVaultDialog)(nil)
var _ paster = (*credentialVaultDialog)(nil)
