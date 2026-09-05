package vault_test

import (
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

func TestVaultAPIKey_RoundTripThroughStore(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	v, err := vault.Open(t.TempDir(), fixedPass("pw"))
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
