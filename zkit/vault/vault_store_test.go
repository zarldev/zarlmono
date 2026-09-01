package vault_test

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A raw $ZARLCODE_KEY takes precedence over an already-initialised
// passphrase vault.
func TestVault_EnvKeyOverridesPassphrase(t *testing.T) {
	tmpHome(t)
	if _, err := vault.Open(fixedPass("pw")); err != nil {
		t.Fatalf("passphrase setup: %v", err)
	}
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open with env key: %v", err)
	}
	ct, nonce, err := v.Encrypt("hi")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Decrypt(ct, nonce); err != nil || got != "hi" {
		t.Errorf("env-key roundtrip: got %q err=%v", got, err)
	}
}

func TestVault_EnvKeyBadLength(t *testing.T) {
	tmpHome(t)
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString([]byte("too short")))
	if _, err := vault.Open(nil); err == nil {
		t.Fatal("expected error for wrong-length env key")
	}
}

func TestVault_EnvKeyBadBase64(t *testing.T) {
	tmpHome(t)
	t.Setenv("ZARLCODE_KEY", "$$$ not base64 $$$")
	if _, err := vault.Open(nil); err == nil {
		t.Fatal("expected error for invalid base64 env key")
	}
}

func TestVaultAPIKey_RoundTripThroughStore(t *testing.T) {
	tmpHome(t)
	store := openTestStore(t)
	v, err := vault.Open(fixedPass("pw"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.GetAPIKeyExact(t.Context(), "", "anthropic")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	want := "sk-ant-secret-12345"
	ct, nonce, err := v.Encrypt(want)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := store.SetAPIKey(t.Context(), "", "anthropic", db.APIKeyCiphertext{
		Ciphertext: ct,
		Nonce:      nonce,
		KeyVersion: vault.CurrentKeyVersion,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, err := store.GetAPIKeyExact(t.Context(), "", "anthropic")
	if err != nil {
		t.Fatalf("get: err=%v", err)
	}
	got, err := v.Decrypt(row.Ciphertext, row.Nonce)
	if err != nil || got != want {
		t.Errorf("decrypt: got=%q err=%v", got, err)
	}

	row, err = store.GetAPIKey(t.Context(), "/tmp/somewhere", "anthropic")
	if err != nil {
		t.Fatalf("workspace fallback: err=%v", err)
	}
	got, err = v.Decrypt(row.Ciphertext, row.Nonce)
	if err != nil || got != want {
		t.Errorf("workspace fallback decrypt: got=%q err=%v", got, err)
	}
}
