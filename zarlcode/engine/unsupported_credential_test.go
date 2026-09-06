package engine_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestOpenSettingsPreservesUnsupportedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := db.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyCredential(t, dir, "openai", "legacy-secret")

	// Headless startup must remain available without unlock input. The atomic
	// migration is deferred and the encrypted credential remains locked.
	locked, err := engine.OpenSettings(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("non-interactive startup: %v", err)
	}
	if _, err := locked.Svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai"); !errors.Is(err, prefs.ErrCredentialsLocked) {
		t.Fatalf("locked key lookup = %v, want ErrCredentialsLocked", err)
	}
	row, err := locked.Store.GetAPIKeyExact(t.Context(), "", "openai")
	if err != nil || row.Storage != db.APIKeyStorageVault {
		t.Fatalf("deferred row = %#v, %v", row, err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.kdf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-interactive startup created KDF material: %v", err)
	}

	// Startup never decrypts or rewrites unsupported credential versions.
	unlocked, err := engine.OpenSettings(t.Context(), t.TempDir(), func(bool, bool) (string, error) {
		return "new-passphrase", nil
	})
	if err != nil {
		t.Fatalf("interactive migration: %v", err)
	}
	if _, err := unlocked.Svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai"); !errors.Is(err, prefs.ErrUnsupportedCredentialFormat) {
		t.Fatalf("unsupported key = %v", err)
	}
	mode, err := unlocked.Svc.CredentialProtection(t.Context())
	if err != nil || mode != prefs.CredentialProtectionPassphrase {
		t.Fatalf("migrated mode = %q, %v", mode, err)
	}
	row, err = unlocked.Store.GetAPIKeyExact(t.Context(), "", "openai")
	if err != nil || row.Storage != db.APIKeyStorageVault || row.KeyVersion != 1 {
		t.Fatalf("migrated row = %#v, %v", row, err)
	}
	if err := unlocked.Close(); err != nil {
		t.Fatal(err)
	}
	if key, err := os.ReadFile(filepath.Join(dir, "master.key")); err != nil || !bytes.Equal(key, bytes.Repeat([]byte{0x5a}, 32)) {
		t.Fatalf("unsupported key material changed: %v", err)
	}
}

func seedLegacyCredential(t *testing.T, dir, provider, plaintext string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x5a}, 32)
	if err := os.WriteFile(filepath.Join(dir, "master.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKey(t.Context(), "", provider, db.APIKeyCiphertext{
		Ciphertext: aead.Seal(nil, nonce, []byte(plaintext), nil),
		Nonce:      nonce,
		KeyVersion: 1,
		Storage:    db.APIKeyStorageVault,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
