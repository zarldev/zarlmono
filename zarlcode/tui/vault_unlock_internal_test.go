package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func vaultUnlockTextKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: string(r), Code: r}
}

func typeVaultUnlockText(m *vaultUnlockModel, text string) {
	for _, r := range text {
		m.handleKey(vaultUnlockTextKey(r))
	}
}

func TestVaultUnlockMasksPassphraseInRender(t *testing.T) {
	m := newVaultUnlockModel(false, false)
	m.width = 100
	m.height = 30
	typeVaultUnlockText(m, "secret")

	got := m.View().Content
	if strings.Contains(got, "secret") {
		t.Fatalf("vault unlock render leaked passphrase: %q", got)
	}
	if !strings.Contains(got, "••••••") {
		t.Fatalf("vault unlock render did not show masked bullets: %q", got)
	}
}

func TestVaultUnlockSubmitExistingVault(t *testing.T) {
	m := newVaultUnlockModel(false, false)
	typeVaultUnlockText(m, "secret")
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if cmd == nil {
		t.Fatal("enter with passphrase should quit the unlock splash")
	}
	if !m.done {
		t.Fatal("enter with passphrase should mark unlock done")
	}
	if m.out != "secret" {
		t.Fatalf("passphrase = %q, want secret", m.out)
	}
}

func TestVaultUnlockSetupRequiresMatchingConfirmation(t *testing.T) {
	m := newVaultUnlockModel(true, false)
	typeVaultUnlockText(m, "secret")
	if cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("first setup enter should move to confirmation, not quit")
	}
	if m.field != vaultUnlockFieldConfirm {
		t.Fatalf("field = %v, want confirm", m.field)
	}
	typeVaultUnlockText(m, "wrong")
	if cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("mismatched confirmation should not quit")
	}
	if m.done {
		t.Fatal("mismatched confirmation marked unlock done")
	}
	if m.err != "passphrases did not match" {
		t.Fatalf("err = %q, want mismatch message", m.err)
	}
	if len(m.confirm) != 0 {
		t.Fatalf("confirmation after mismatch = %q, want cleared", string(m.confirm))
	}

	typeVaultUnlockText(m, "secret")
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("matching confirmation should quit")
	}
	if !m.done || m.out != "secret" {
		t.Fatalf("done/out = %v/%q, want true/secret", m.done, m.out)
	}
}

func TestVaultUnlockRetryShowsWrongPassphraseMessage(t *testing.T) {
	m := newVaultUnlockModel(false, true)
	lines := strings.Join(m.infoLines(), "\n")
	if !strings.Contains(lines, "passphrase incorrect") {
		t.Fatalf("retry info lines = %q, want incorrect-passphrase message", lines)
	}
}

func TestVaultUnlockCancelReturnsError(t *testing.T) {
	m := newVaultUnlockModel(false, false)
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("escape should quit the unlock splash")
	}
	if m.done {
		t.Fatal("escape marked unlock done")
	}
}
