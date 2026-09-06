package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/zapp"
)

func updateVault(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	got, _ := m.Update(msg)
	return got
}

func typeVault(t *testing.T, m tea.Model, text string) tea.Model {
	t.Helper()
	for _, r := range text {
		m = updateVault(t, m, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	return m
}

func TestVaultUnlockMasksPassphraseInRender(t *testing.T) {
	m := tui.NewVaultUnlockModel(false, false)
	m = updateVault(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = typeVault(t, m, "secret")
	out := m.View().Content
	if strings.Contains(out, "secret") || !strings.Contains(out, "••••••") {
		t.Fatalf("passphrase not masked: %q", out)
	}
}

func TestVaultUnlockSubmitAndSetupConfirmation(t *testing.T) {
	m := tui.NewVaultUnlockModel(false, false)
	m = updateVault(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = typeVault(t, m, "secret")
	m = updateVault(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View().Content, "••••••") {
		t.Fatal("submitted passphrase should remain masked")
	}

	setup := tui.NewVaultUnlockModel(true, false)
	setup = updateVault(t, setup, tea.WindowSizeMsg{Width: 100, Height: 30})
	setup = typeVault(t, setup, "secret")
	setup = updateVault(t, setup, tea.KeyPressMsg{Code: tea.KeyEnter})
	setup = typeVault(t, setup, "wrong")
	setup = updateVault(t, setup, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(setup.View().Content, "passphrases did not match") {
		t.Fatal("mismatch feedback missing")
	}
	setup = typeVault(t, setup, "secret")
	updateVault(t, setup, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestVaultUnlockRetryShowsWrongPassphraseMessage(t *testing.T) {
	m := tui.NewVaultUnlockModel(false, true)
	m = updateVault(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(m.View().Content, "passphrase incorrect") {
		t.Fatal("retry feedback missing")
	}
}

func TestStartupVaultUnlockEscapeExits(t *testing.T) {
	m := tui.NewStartupVaultUnlockModelForTest(false, false)
	m = updateVault(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	out := m.View().Content
	if !strings.Contains(out, "esc exit") || strings.Contains(out, "skip vault") {
		t.Fatalf("startup cancellation hint = %q", out)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("escape did not stop the startup vault program")
	}
}

func TestCancelledVaultStartupReturnsSuccessWithoutOpeningIntro(t *testing.T) {
	got := (tui.Launch{}).Run(t.Context(), nil, tui.NewStartupCancelledForTest())
	if got != zapp.ExitOK {
		t.Fatalf("cancelled startup exit code = %d, want %d", got, zapp.ExitOK)
	}
}
