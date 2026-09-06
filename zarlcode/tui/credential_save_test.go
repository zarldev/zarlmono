package tui_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func newCredentialUI(t *testing.T) (*tui.UI, *engine.Settings, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := db.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	ui := tui.New()
	ui.SetSettings(settings)
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*tui.UI), settings, dbPath
}

func typeCredential(t *testing.T, ui *tui.UI, text string) {
	t.Helper()
	for _, r := range text {
		if _, cmd := ui.Update(tea.KeyPressMsg{Code: r, Text: string(r)}); cmd != nil {
			t.Fatal("typing unexpectedly started a command")
		}
	}
}

func TestFirstCredentialSaveUsesVaultModalAndCiphertext(t *testing.T) {
	ui, settings, dbPath := newCredentialUI(t)
	const secret = "api-secret-canary"
	var saveErr error
	if cmd := ui.SaveCredential("openai", secret, func(err error) { saveErr = err }); cmd != nil {
		t.Fatal("opening vault modal returned async work before passphrase submission")
	}
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "new passphrase") || strings.Contains(view, secret) {
		t.Fatalf("first credential did not open masked setup modal:\n%s", view)
	}

	typeCredential(t, ui, "correct horse")
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeCredential(t, ui, "wrong")
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "passphrases did not match") {
		t.Fatalf("passphrase mismatch did not stay in modal:\n%s", view)
	}
	typeCredential(t, ui, "correct horse")
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmed passphrase did not start owned vault command")
	}
	_, _ = ui.Update(cmd())
	if saveErr != nil {
		t.Fatalf("save callback: %v", saveErr)
	}
	got, err := settings.Svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai")
	if err != nil || got != secret {
		t.Fatalf("stored credential = %q, %v", got, err)
	}
	stored, err := settings.Store.GetAPIKey(t.Context(), "", "openai")
	if err != nil || stored.Storage != db.APIKeyStorageVault || string(stored.Ciphertext) == secret {
		t.Fatalf("stored row = %#v, %v", stored, err)
	}
	mode, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCredentialProtection)
	if err != nil || mode.Value != prefs.CredentialProtectionPassphrase {
		t.Fatalf("credential protection = %#v, %v", mode, err)
	}
	blob, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatal("database file contains plaintext credential canary")
	}
}

func TestFirstCredentialSaveCancellationPreservesStorage(t *testing.T) {
	ui, settings, dbPath := newCredentialUI(t)
	const secret = "cancelled-secret-canary"
	var saveErr error
	ui.SaveCredential("openai", secret, func(err error) { saveErr = err })
	typeCredential(t, ui, "never persisted")
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		t.Fatal("cancelling vault setup started a command")
	}
	if saveErr == nil {
		t.Fatal("cancellation was not reported")
	}
	if rows, err := settings.Svc.ListKeys(t.Context(), prefs.ScopeGlobal); err != nil || len(rows) != 0 {
		t.Fatalf("credential rows after cancellation = %v, %v", rows, err)
	}
	if _, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCredentialProtection); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("credential protection setting after cancellation = %v", err)
	}
	if exists, err := settings.CredentialVaultExists(); err != nil || exists {
		t.Fatalf("vault material after cancellation exists=%v, err=%v", exists, err)
	}
	blob, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatal("cancelled plaintext credential reached database")
	}
}
