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

// oauthDoneMsg / oauthFailedMsg carry an OAuth flow's outcome back to the
// Update loop, which routes them to the open providers dialog.
type oauthDoneMsg struct {
	provider string
	account  string
}

type oauthFailedMsg struct {
	provider string
	err      error
}

// startOAuthLogin builds the authorization flow, shows + opens its URL, and
// returns a command that blocks on the loopback callback (exchange + store
// handled by codex.AwaitCallback) and reports the result. Codex only
// for now; claude-code login is offered via the CLI until its in-TUI flow
// lands.
func (m *UI) startOAuthLogin(provider string) tea.Cmd {
	pd, _ := topProvidersDialog(m)
	fail := func(err error) tea.Cmd {
		return func() tea.Msg { return oauthFailedMsg{provider: provider, err: err} }
	}

	if m.settings == nil || m.settings.Svc == nil {
		if pd != nil {
			pd.status = "credential service unavailable"
		}
		return nil
	}
	svc := m.settings.Svc
	parent := m.appContext()
	switch id, _ := llm.ParseLLMProvider(provider); id {
	case backends.NameOpenAICodex:
		flow, err := openaicodex.CreateAuthorizationFlow()
		if err != nil {
			return fail(err)
		}
		if pd != nil {
			pd.beginOAuth(flow.URL)
		}
		openBrowser(flow.URL) // best-effort; the URL is shown + copied for manual use
		await := func() tea.Msg {
			ctx, cancel := context.WithTimeout(parent, oauthLoginTimeout)
			defer cancel()
			account, err := codex.AwaitCallback(ctx, svc, flow)
			if err != nil {
				return oauthFailedMsg{provider: provider, err: err}
			}
			return oauthDoneMsg{provider: provider, account: account}
		}
		// Copy the (long) URL to the clipboard so the user can paste it even
		// when the browser didn't open and the line wraps off-screen.
		return tea.Batch(tea.SetClipboard(flow.URL), await)
	case backends.NameClaudeCode:
		// Claude Code signs in via `claude setup-token` (its own browser
		// flow). Run it attached to the terminal — tea.ExecProcess suspends
		// the alt-screen — and capture stdout to extract + store the token.
		// No manual CLI step for the user.
		if pd != nil {
			pd.status = "running `claude setup-token` — complete the browser sign-in…"
		}
		buf := &bytes.Buffer{}
		cmd := claude.SetupTokenCommand()
		cmd.Stdin = os.Stdin
		cmd.Stdout = io.MultiWriter(os.Stdout, buf) // user sees it; we capture it
		cmd.Stderr = os.Stderr
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return oauthFailedMsg{provider: provider, err: err}
			}
			if serr := claude.StoreToken(parent, svc, buf.String()); serr != nil {
				return oauthFailedMsg{provider: provider, err: serr}
			}
			return oauthDoneMsg{provider: provider}
		})
	default:
		if pd != nil {
			pd.status = provider + ": in-TUI sign-in not available"
		}
		return nil
	}
}

// handleOAuthMsg routes OAuth results to the open providers dialog. Returns
// true when it consumed the message.
func (m *UI) handleOAuthMsg(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case oauthDoneMsg:
		if pd, ok := topProvidersDialog(m); ok {
			pd.onOAuthResult(msg.account, nil)
		}
		return true
	case oauthFailedMsg:
		if pd, ok := topProvidersDialog(m); ok {
			pd.onOAuthResult("", msg.err)
		}
		return true
	}
	return false
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

// openBrowser best-effort opens url in the user's default browser. Failures
// are silent — the URL is also shown in the dialog for manual copy. It's a
// var so tests can stub it out (running it would spawn a real browser).
var openBrowser = func(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
