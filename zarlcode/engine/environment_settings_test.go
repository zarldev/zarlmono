package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func TestOpenSettingsIgnoresCredentialEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ZARLCODE_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("ZARLCODE_PASSPHRASE", "explicit-passphrase")
	root := t.TempDir()
	ctx := t.Context()
	settings, err := engine.OpenSettings(ctx, root, func(bool, bool) (string, error) {
		t.Error("ambient credentials caused a fresh installation to prompt")
		return "unexpected", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Close() })
	mode, err := settings.Svc.CredentialProtection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Svc.HasVault() || mode != prefs.CredentialProtectionOff {
		t.Fatal("environment enabled credential protection")
	}
	if err := settings.Svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	stored, err := settings.Store.GetAPIKey(ctx, "", "openai")
	if err != nil || stored.Storage != db.APIKeyStoragePlaintext {
		t.Fatalf("fresh credential storage = %v, %v", stored.Storage, err)
	}
	dir, err := db.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(dir, func(bool, bool) (string, error) { return "explicit-passphrase", nil })
	if err != nil {
		t.Fatal(err)
	}
	settings.Svc.SetVault(v)
	if _, err := settings.Svc.EnableCredentialProtection(ctx); err != nil {
		t.Fatal(err)
	}
	if err := settings.Close(); err != nil {
		t.Fatal(err)
	}

	locked, err := engine.OpenSettings(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locked.Close() })
	if locked.Svc.HasVault() {
		t.Fatal("environment unlocked protected credentials")
	}
	if _, err := locked.Svc.GetKey(ctx, prefs.ScopeGlobal, "openai"); !errors.Is(err, prefs.ErrCredentialsLocked) {
		t.Fatalf("locked key lookup = %v", err)
	}
	if _, err := locked.Svc.DisableCredentialProtection(ctx); !errors.Is(err, prefs.ErrCredentialsLocked) {
		t.Fatalf("locked protection disable = %v", err)
	}
	mode, err = locked.Svc.CredentialProtection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != prefs.CredentialProtectionPassphrase {
		t.Fatal("failed disable changed database protection mode")
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ZARLCODE_PASSPHRASE", "wrong-ambient-passphrase")
	unlocked, err := engine.OpenSettings(ctx, root, func(bool, bool) (string, error) { return "explicit-passphrase", nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unlocked.Close() })
	if got, err := unlocked.Svc.GetKey(ctx, prefs.ScopeGlobal, "openai"); err != nil || got != "stored-secret" {
		t.Fatalf("explicit unlock = %q, %v", got, err)
	}
}

func TestSearxngURLUsesDatabaseNotEnvironment(t *testing.T) {
	t.Setenv("SEARXNG_URL", "http://ambient.invalid")
	t.Setenv("HOME", t.TempDir())
	settings, err := engine.OpenSettings(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Close() })
	if got := settings.SearxngURL(t.Context()); got != engine.DefaultSearxngURL {
		t.Fatalf("unset URL = %q, want application default", got)
	}
	const stored = "http://configured.test"
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeySearxngURL, stored); err != nil {
		t.Fatal(err)
	}
	if got := settings.SearxngURL(t.Context()); got != stored {
		t.Fatalf("configured URL = %q, want %q", got, stored)
	}
}
