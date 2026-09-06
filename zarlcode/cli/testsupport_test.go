package cli_test

import (
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

func openTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Open(t.TempDir(), func(bool, bool) (string, error) { return "test-passphrase", nil })
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}
