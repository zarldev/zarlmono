package prefs_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestMigrateCredentialProtectionConsumesLegacyMarker(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	svc := prefs.NewService(store, openTestVault(t), t.TempDir())
	if _, err := svc.EnableCredentialProtection(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetKey(t.Context(), prefs.ScopeGlobal, "provider", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSetting(t.Context(), prefs.ScopeGlobal, "credential_protection"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(t.Context(), prefs.ScopeGlobal, "vault_prompt", "off"); err != nil {
		t.Fatal(err)
	}
	n, err := svc.MigrateCredentialProtection(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("MigrateCredentialProtection = %d, %v; want 1, nil", n, err)
	}
	row, err := store.GetAPIKeyExact(t.Context(), "", "provider")
	if err != nil || row.Storage != db.APIKeyStoragePlaintext || string(row.Ciphertext) != "secret" {
		t.Fatalf("migrated row = %#v, %v", row, err)
	}
	mode, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, "credential_protection")
	if err != nil || mode.Value != prefs.CredentialProtectionOff {
		t.Fatalf("mode = %#v, %v", mode, err)
	}
	if _, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, "vault_prompt"); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("legacy marker remains: %v", err)
	}
	n, err = svc.MigrateCredentialProtection(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("idempotent migration = %d, %v", n, err)
	}
}

func TestCredentialProtectionIsDatabaseWide(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	workspace := t.TempDir()
	svc := prefs.NewService(store, openTestVault(t), workspace)
	if _, err := svc.EnableCredentialProtection(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A stale or manually written workspace preference must not override the
	// global storage policy or trigger the legacy global migration.
	if err := svc.SetSetting(t.Context(), prefs.ScopeWorkspace, "credential_protection", prefs.CredentialProtectionOff); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(t.Context(), prefs.ScopeWorkspace, "vault_prompt", prefs.CredentialProtectionOff); err != nil {
		t.Fatal(err)
	}
	mode, err := svc.CredentialProtection(t.Context())
	if err != nil || mode != prefs.CredentialProtectionPassphrase {
		t.Fatalf("CredentialProtection = %q, %v; want passphrase, nil", mode, err)
	}
	if err := svc.SetKey(t.Context(), prefs.ScopeWorkspace, "provider", "secret"); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAPIKeyExact(t.Context(), workspace, "provider")
	if err != nil || row.Storage != db.APIKeyStorageVault {
		t.Fatalf("workspace row storage = %v, %v; want vault, nil", row.Storage, err)
	}
	if n, err := svc.MigrateCredentialProtection(t.Context()); err != nil || n != 0 {
		t.Fatalf("workspace legacy marker migration = %d, %v; want 0, nil", n, err)
	}
	mode, err = svc.CredentialProtection(t.Context())
	if err != nil || mode != prefs.CredentialProtectionPassphrase {
		t.Fatalf("mode after ignored workspace migration = %q, %v", mode, err)
	}
}
