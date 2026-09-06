package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSetupCredentialVaultAndSetKeyKeepsInitializedVaultAfterCanceledDatabaseWrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := t.Context()

	store, err := db.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	const passphrase = "durable-vault-passphrase"
	if err := settings.SetupCredentialVaultAndSetKey(canceled, passphrase, prefs.ScopeGlobal, "openai", "must-not-persist"); !errors.Is(err, context.Canceled) {
		t.Fatalf("setup error = %v, want context.Canceled", err)
	}
	if settings.Svc.HasVault() {
		t.Fatal("failed setup attached a usable vault")
	}
	if _, err := store.GetAPIKeyExact(ctx, "", "openai"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("failed setup key row error = %v, want db.ErrNotFound", err)
	}

	dir, err := db.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.kdf")); err != nil {
		t.Fatalf("initialized master.kdf did not persist: %v", err)
	}
	if exists, err := settings.CredentialVaultExists(); err != nil || !exists {
		t.Fatalf("CredentialVaultExists = %v, %v; want true, nil", exists, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	freshStore, err := db.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = freshStore.Close() })
	fresh := engine.NewSettings(freshStore, nil, nil, t.TempDir())
	if exists, err := fresh.CredentialVaultExists(); err != nil || !exists {
		t.Fatalf("fresh CredentialVaultExists = %v, %v; want true, nil", exists, err)
	}
	if err := fresh.SetupCredentialVault(ctx, passphrase); err != nil {
		t.Fatalf("unlock initialized vault with same passphrase: %v", err)
	}
	if !fresh.Svc.HasVault() {
		t.Fatal("successful explicit unlock did not attach the vault")
	}
}
