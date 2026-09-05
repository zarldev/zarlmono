package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

// handlePaste feeds clipboard content to whichever text-entry sub-mode is
// open: the inline row editor, or the focused detail panel (providers
// key/add form, mcp add form). Every settings field is single-line, so
// embedded newlines are stripped — a pasted API key carrying a trailing
// newline lands clean. No-op when nothing is in a text-entry state.
func (d *settingsDialog) handlePaste(content string) {
	content = stripNewlines(content)
	if content == "" {
		return
	}
	switch {
	case d.editing:
		d.editor.insert(content)
	case d.focusRows && d.cats[d.cat].providers:
		d.providers.handlePaste(content)
	case d.focusRows && d.cats[d.cat].mcp:
		d.mcp.handlePaste(content)
	}
}

// stripNewlines removes CR/LF so pasted clipboard content stays single-line
// for the settings fields (API keys, urls, model ids).
func stripNewlines(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func (d *settingsDialog) handleEdit(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc":
		d.editing = false
	case "enter":
		val := d.editor.submit()
		if d.curRow().kind == rowKey {
			d.commitCred(d.curRow().cred, strings.TrimSpace(val))
			d.editing = false
			return actionNone{}
		}
		if row := d.curRow(); row.numeric && val != "" {
			n, err := strconv.Atoi(val)
			switch {
			case err != nil || n < 0:
				d.setStatus(row.label + ": enter a non-negative integer")
				return actionNone{}
			case row.max > 0 && n > row.max:
				d.setStatus(fmt.Sprintf("%s: maximum is %d", row.label, row.max))
				return actionNone{}
			}
		}
		d.commit(d.curRow().key, val)
		d.editing = false
	case "backspace":
		d.editor.backspace()
	case "left":
		d.editor.left()
	case "right":
		d.editor.right()
	default:
		if msg.Text != "" {
			d.editor.insert(msg.Text)
		}
	}
	return actionNone{}
}

// commit persists val at workspace scope (or clears the row when empty),
// records a status badge, and refreshes the view.
func (d *settingsDialog) commit(key, val string) {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	ctx := d.ctx
	if key == prefs.KeyCredentialProtection {
		switch val {
		case prefs.CredentialProtectionOff:
			if n, err := d.s.Svc.DisableCredentialProtection(ctx); err != nil {
				d.setStatus("credential protection: " + err.Error())
			} else {
				d.setStatus(fmt.Sprintf("credential protection off — %d key(s) plaintext", n))
			}
		case prefs.CredentialProtectionPassphrase:
			if !d.s.Svc.HasVault() {
				d.setStatus("enable with: zarlcode keys protect on")
				return
			}
			if n, err := d.s.Svc.EnableCredentialProtection(ctx); err != nil {
				d.setStatus("credential protection: " + err.Error())
			} else {
				d.setStatus(fmt.Sprintf("credential protection enabled — %d key(s) encrypted", n))
			}
		default:
			d.setStatus("credential protection: invalid value " + val)
		}
		d.refresh(ctx)
		return
	}
	switch val {
	case "":
		if err := d.s.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, key); err != nil {
			d.setStatus("error: " + err.Error())
		} else {
			d.setStatus(key + " cleared")
		}
	default:
		if err := d.s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, key, val); err != nil {
			d.setStatus("error: " + err.Error())
		} else {
			d.setStatus(key + " → " + val + " (workspace)")
		}
	}
	d.refresh(ctx)
}

// commitCred persists a vault-stored credential at global scope (or clears it
// when empty), records a status badge, and refreshes the view. Credentials are
// account-level, so — like the providers panel and `zarlcode keys set` — they
// never pin to a single workspace. It's the rowKey counterpart to commit.
func (d *settingsDialog) commitCred(provider, val string) {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	ctx := d.ctx
	switch val {
	case "":
		if err := d.s.Svc.DeleteKey(ctx, prefs.ScopeGlobal, provider); err != nil {
			d.setStatus("clear key: " + err.Error())
		} else {
			d.setStatus(provider + " key cleared")
		}
	default:
		if err := d.s.Svc.SetKey(ctx, prefs.ScopeGlobal, provider, val); err != nil {
			d.setStatus("save key: " + err.Error())
		} else {
			d.setStatus(provider + " key saved (global)")
		}
	}
	d.refresh(ctx)
}

func (d *settingsDialog) commitModelSelection(selection prefs.ModelSelection) {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	if err := d.s.Svc.SetModelSelection(d.ctx, prefs.ScopeWorkspace, selection); err != nil {
		d.setStatus("error: " + err.Error())
		return
	}
	d.setStatus(prefs.KeyProvider + " → " + selection.Provider + " (workspace)")
	d.refresh(d.ctx)
}

func (d *settingsDialog) promote() {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	r := d.curRow()
	if r.kind == rowKey {
		d.setStatus("credentials are stored globally")
		return
	}
	if !r.isSet {
		d.setStatus("using built-in default; no workspace override to move")
		return
	}
	if r.scope != prefs.ScopeWorkspace {
		d.setStatus("already using the global default")
		return
	}
	ctx := d.ctx
	if err := d.s.Svc.PromoteSetting(ctx, r.key); err != nil {
		d.setStatus("promote: " + err.Error())
	} else {
		d.setStatus(r.label + " moved to global default")
	}
	d.refresh(ctx)
}

func indexOf(ss []string, v string) int {
	for i, s := range ss {
		if s == v {
			return i
		}
	}
	return -1
}
