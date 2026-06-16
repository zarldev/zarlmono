package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestProvidersDialog_OAuthBadgeAndEnter(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)
	if !gotoProvider(d, backends.NameOpenAICodex.String()) {
		t.Fatal("openai-codex builtin missing")
	}

	// Not signed in → enter requests an OAuth login (not a key edit).
	act := d.handleKey(skey(tea.KeyEnter))
	if lg, ok := act.(actionOAuthLogin); !ok || lg.provider != backends.NameOpenAICodex.String() {
		t.Fatalf("enter on codex should request OAuth login, got %T", act)
	}
	if d.editing {
		t.Error("codex enter must not open the API-key editor")
	}
}

func TestProvidersDialog_OAuthLifecycle(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)
	gotoProvider(d, backends.NameOpenAICodex.String())

	d.beginOAuth("https://auth.openai.com/oauth/authorize?x=1")
	if !d.oauthBusy || d.oauthURL == "" {
		t.Fatal("beginOAuth should set the awaiting state + URL")
	}

	d.onOAuthResult("acct-123", nil)
	if d.oauthBusy {
		t.Error("onOAuthResult should clear the awaiting state")
	}
	if d.status == "" {
		t.Error("expected a success status after sign-in")
	}

	// Failure path keeps the error visible.
	d.beginOAuth("https://x")
	d.onOAuthResult("", errors.New("port in use"))
	if d.oauthBusy {
		t.Error("failure should also clear the awaiting state")
	}
}

func TestStartOAuthLogin_RequiresCredentialService(t *testing.T) {
	m := New()

	if cmd := m.startOAuthLogin(backends.NameOpenAICodex.String()); cmd != nil {
		t.Error("login without settings should not return a command")
	}
}

func TestStartOAuthLogin_BuildsFlowAndAwaits(t *testing.T) {
	prev := openBrowser
	openBrowser = func(string) {} // don't spawn a real browser
	defer func() { openBrowser = prev }()

	s := newTestSettingsWithVault(t)
	m := New()
	m.SetSettings(s)
	pd := newProvidersDialog(s)
	gotoProvider(pd, backends.NameOpenAICodex.String())
	m.overlay.push(pd)

	cmd := m.startOAuthLogin(backends.NameOpenAICodex.String())
	if cmd == nil {
		t.Fatal("codex login with a vault should return an await command")
	}
	if !pd.oauthBusy || pd.oauthURL == "" {
		t.Error("login should put the dialog into the awaiting state with a URL")
	}
	// Do NOT run cmd — it would bind :1455 and block on the network.
}

func TestHandleOAuthMsg_RoutesToDialog(t *testing.T) {
	s := newTestSettingsWithVault(t)
	m := New()
	m.SetSettings(s)
	pd := newProvidersDialog(s)
	pd.beginOAuth("https://x")
	m.overlay.push(pd)

	if !m.handleOAuthMsg(oauthDoneMsg{provider: backends.NameOpenAICodex.String(), account: "acct"}) {
		t.Fatal("oauthDoneMsg should be consumed")
	}
	if pd.oauthBusy {
		t.Error("done message should clear the dialog's awaiting state")
	}
}
