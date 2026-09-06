package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
)

// oauthLoginTimeout bounds how long we hold the loopback callback listener
// waiting for the browser redirect before giving up.
const oauthLoginTimeout = 3 * time.Minute

// oauthDoneMsg / oauthFailedMsg carry one identified OAuth attempt's outcome
// back to the Update loop.
type oauthDoneMsg struct {
	id       uint64
	provider string
	account  string
}

type oauthFailedMsg struct {
	id       uint64
	provider string
	err      error
}

type oauthOperation struct {
	id       uint64
	provider string
	owner    *providersDialog
	cancel   context.CancelFunc
}

// startOAuthLogin starts the selected provider's authentication flow. Codex
// owns a cancellable loopback listener; Claude hands the terminal to its CLI,
// where completion or interruption is owned by that subprocess.
func (m *UI) startOAuthLogin(provider string) tea.Cmd {
	pd, ok := topProvidersDialog(m)
	if !ok {
		return nil
	}
	if m.settings == nil || m.settings.Svc == nil {
		pd.status = "credential service unavailable"
		return nil
	}
	svc := m.settings.Svc
	parent := m.appContext()
	switch id, _ := llm.ParseLLMProvider(provider); id {
	case backends.NameOpenAICodex:
		flow, err := openaicodex.CreateAuthorizationFlow()
		if err != nil {
			pd.onOAuthResult("", err)
			return nil
		}
		ctx, cancel := context.WithTimeout(parent, oauthLoginTimeout)
		attemptID := m.beginOAuthOperation(provider, pd, cancel)
		pd.beginOAuth(flow.URL)
		browser := openBrowser(flow.URL) // best-effort; the URL is shown + copied for manual use
		await := func() tea.Msg {
			account, err := codex.AwaitCallback(ctx, svc, flow)
			if err != nil {
				return oauthFailedMsg{id: attemptID, provider: provider, err: err}
			}
			return oauthDoneMsg{id: attemptID, provider: provider, account: account}
		}
		// Copy the (long) URL to the clipboard so the user can paste it even
		// when the browser didn't open and the line wraps off-screen.
		return tea.Batch(tea.SetClipboard(flow.URL), browser, await)
	case backends.NameClaudeCode:
		attemptID := m.beginOAuthOperation(provider, pd, nil)
		// tea.ExecProcess suspends Bubble Tea input while the CLI owns the
		// terminal, so cancellation is performed in the Claude CLI itself.
		pd.status = "running `claude setup-token` — complete or cancel in the terminal…"
		buf := &bytes.Buffer{}
		cmd := claude.SetupTokenCommand()
		cmd.Stdin = os.Stdin
		cmd.Stdout = io.MultiWriter(os.Stdout, buf)
		cmd.Stderr = io.MultiWriter(os.Stderr, buf)
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return oauthFailedMsg{id: attemptID, provider: provider, err: err}
			}
			if serr := claude.StoreToken(parent, svc, buf.String()); serr != nil {
				return oauthFailedMsg{id: attemptID, provider: provider, err: serr}
			}
			return oauthDoneMsg{id: attemptID, provider: provider}
		})
	default:
		pd.status = provider + ": in-TUI sign-in not available"
		return nil
	}
}

func (m *UI) beginOAuthOperation(provider string, owner *providersDialog, cancel context.CancelFunc) uint64 {
	m.cancelOAuthOperation(false)
	m.oauthSeq++
	m.oauthOperation = &oauthOperation{id: m.oauthSeq, provider: provider, owner: owner, cancel: cancel}
	return m.oauthSeq
}

func (m *UI) cancelOAuthOperation(notify bool) {
	op := m.oauthOperation
	if op == nil {
		return
	}
	m.oauthOperation = nil
	if op.cancel != nil {
		op.cancel()
		if notify && op.owner != nil {
			op.owner.onOAuthCancelled()
		}
	}
}

func (m *UI) cancelOAuthForDialog(d dialog) {
	op := m.oauthOperation
	if op == nil || op.owner == nil {
		return
	}
	switch d := d.(type) {
	case *providersDialog:
		if d == op.owner {
			m.cancelOAuthOperation(false)
		}
	case *settingsDialog:
		if d.providers == op.owner {
			m.cancelOAuthOperation(false)
		}
	}
}

func (m *UI) oauthOwnerOpen(owner *providersDialog) bool {
	for _, d := range m.overlay.stack {
		switch d := d.(type) {
		case *providersDialog:
			if d == owner {
				return true
			}
		case *settingsDialog:
			if d.providers == owner {
				return true
			}
		}
	}
	return false
}

// handleOAuthMsg accepts only the current attempt's result. Late results from
// cancelled, superseded, or dismissed flows are consumed without UI mutation.
func (m *UI) handleOAuthMsg(msg tea.Msg) bool {
	var id uint64
	var provider, account string
	var resultErr error
	switch msg := msg.(type) {
	case oauthDoneMsg:
		id, provider, account = msg.id, msg.provider, msg.account
	case oauthFailedMsg:
		id, provider, resultErr = msg.id, msg.provider, msg.err
	default:
		return false
	}
	op := m.oauthOperation
	if op == nil || op.id != id || op.provider != provider {
		return true
	}
	m.oauthOperation = nil
	if op.cancel != nil {
		op.cancel()
	}
	if m.oauthOwnerOpen(op.owner) {
		op.owner.onOAuthResult(account, resultErr)
	}
	return true
}

// topProvidersDialog finds the open providers panel: the one embedded in the
// settings overlay (the normal path), or a directly-pushed providersDialog
// (standalone, used in tests).
func topProvidersDialog(m *UI) (*providersDialog, bool) {
	for i := len(m.overlay.stack) - 1; i >= 0; i-- {
		switch d := m.overlay.stack[i].(type) {
		case *providersDialog:
			return d, true
		case *settingsDialog:
			if d.providers != nil {
				return d.providers, true
			}
		}
	}
	return nil, false
}

// openBrowser best-effort opens url in the user's default browser. The returned
// command owns and waits for the subprocess; failures stay silent because the
// dialog also shows the URL for manual copy. The variable lets tests suppress
// the external side effect.
var openBrowser = func(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Run()
		return nil
	}
}
