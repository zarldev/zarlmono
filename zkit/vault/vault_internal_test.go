package vault

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

// homeForTest points HOME at a temp dir so db.DefaultDir (and thus the vault
// material) resolves under test-owned storage.
func homeForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ZARLCODE_KEY", "")
	t.Setenv("ZARLCODE_PASSPHRASE", "")
}

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

func fixedPass(p string) PassphraseFunc {
	return func(_, _ bool) (string, error) { return p, nil }
}

// A raw $ZARLCODE_KEY takes precedence over an already-initialised
// passphrase vault.
func TestVault_EnvKeyOverridesPassphrase(t *testing.T) {
	homeForTest(t)
	if _, err := Open(fixedPass("pw")); err != nil {
		t.Fatalf("passphrase setup: %v", err)
	}
	t.Setenv(masterKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, masterKeySize)))
	v, err := Open(nil)
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
	homeForTest(t)
	t.Setenv(masterKeyEnv, base64.StdEncoding.EncodeToString([]byte("too short")))
	if _, err := Open(nil); err == nil {
		t.Fatal("expected error for wrong-length env key")
	}
}

func TestVault_EnvKeyBadBase64(t *testing.T) {
	homeForTest(t)
	t.Setenv(masterKeyEnv, "$$$ not base64 $$$")
	if _, err := Open(nil); err == nil {
		t.Fatal("expected error for invalid base64 env key")
	}
}

func TestVaultAPIKey_RoundTripThroughStore(t *testing.T) {
	homeForTest(t)
	store := openTestStore(t)
	v, err := Open(fixedPass("pw"))
	if err != nil {
		t.Fatal(err)
	}

	// Initially absent.
	_, ok, err := store.GetAPIKeyExact(t.Context(), "", "anthropic")
	if err != nil || ok {
		t.Errorf("expected absent, got ok=%v err=%v", ok, err)
	}

	// Set + get round-trips through encrypt/decrypt.
	want := "sk-ant-secret-12345"
	ct, nonce, err := v.Encrypt(want)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := store.SetAPIKey(t.Context(), "", "anthropic", db.APIKeyCiphertext{
		Ciphertext: ct,
		Nonce:      nonce,
		KeyVersion: CurrentKeyVersion,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, ok, err := store.GetAPIKeyExact(t.Context(), "", "anthropic")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	got, err := v.Decrypt(row.Ciphertext, row.Nonce)
	if err != nil || got != want {
		t.Errorf("decrypt: got=%q err=%v", got, err)
	}

	// Workspace lookup falls back to global ('').
	row, ok, err = store.GetAPIKey(t.Context(), "/tmp/somewhere", "anthropic")
	if err != nil || !ok {
		t.Fatalf("workspace fallback: ok=%v err=%v", ok, err)
	}
	got, err = v.Decrypt(row.Ciphertext, row.Nonce)
	if err != nil || got != want {
		t.Errorf("workspace fallback decrypt: got=%q err=%v", got, err)
	}
}
