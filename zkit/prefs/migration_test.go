package prefs_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestMigrateCredentialProtectionAppliesEncryptedPolicy(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	svc := prefs.NewService(store, openTestVault(t), t.TempDir())
	if err := store.SetAPIKey(t.Context(), "", "provider", db.APIKeyCiphertext{
		Ciphertext: []byte("secret"),
		Storage:    db.APIKeyStoragePlaintext,
	}); err != nil {
		t.Fatal(err)
	}

	n, err := svc.MigrateCredentialProtection(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("MigrateCredentialProtection = %d, %v; want 1, nil", n, err)
	}
	row, err := store.GetAPIKeyExact(t.Context(), "", "provider")
	if err != nil || row.Storage != db.APIKeyStorageVault || string(row.Ciphertext) == "secret" {
		t.Fatalf("migrated row = %#v, %v", row, err)
	}
	mode, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, "credential_protection")
	if err != nil || mode.Value != prefs.CredentialProtectionPassphrase {
		t.Fatalf("mode = %#v, %v", mode, err)
	}
	n, err = svc.MigrateCredentialProtection(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("idempotent migration = %d, %v", n, err)
	}
}

func TestUnmarkedPlaintextRequiresMigrationUnderEncryptedDefault(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := t.Context()
	if err := store.SetAPIKey(ctx, "", "provider", db.APIKeyCiphertext{
		Ciphertext: []byte("legacy-plaintext"),
		Storage:    db.APIKeyStoragePlaintext,
	}); err != nil {
		t.Fatal(err)
	}

	locked := prefs.NewService(store, nil, "")
	if got, err := locked.GetKey(ctx, prefs.ScopeGlobal, "provider"); !errors.Is(err, prefs.ErrCredentialsLocked) || got != "" {
		t.Fatalf("unmigrated GetKey = %q, %v; want locked", got, err)
	}
	v := openTestVault(t)
	locked.SetVault(v)
	n, err := locked.MigrateCredentialProtection(ctx)
	if err != nil || n != 1 {
		t.Fatalf("MigrateCredentialProtection = %d, %v; want 1, nil", n, err)
	}
	got, err := locked.GetKey(ctx, prefs.ScopeGlobal, "provider")
	if err != nil || got != "legacy-plaintext" {
		t.Fatalf("migrated GetKey = %q, %v", got, err)
	}
	row, err := store.GetAPIKeyExact(ctx, "", "provider")
	if err != nil || row.Storage != db.APIKeyStorageVault || string(row.Ciphertext) == "legacy-plaintext" {
		t.Fatalf("migrated row = %#v, %v", row, err)
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
	// global storage policy.
	if err := svc.SetSetting(t.Context(), prefs.ScopeWorkspace, "credential_protection", prefs.CredentialProtectionOff); err != nil {
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
		t.Fatalf("workspace credential protection migration = %d, %v; want 0, nil", n, err)
	}
	mode, err = svc.CredentialProtection(t.Context())
	if err != nil || mode != prefs.CredentialProtectionPassphrase {
		t.Fatalf("mode after credential protection migration = %q, %v", mode, err)
	}
}
