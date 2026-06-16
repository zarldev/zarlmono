package tui

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func newVaultSettings(t *testing.T) *engine.Settings {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return engine.NewSettings(t.Context(), store, v, "")
}

// A legacy plaintext token in the mcp_servers row must be migrated into
// the vault on first resolve and the column cleared, while still returning
// the token for that launch. A second resolve reads it straight from the
// vault.
func TestResolveMCPAuthToken_MigratesLegacyPlaintext(t *testing.T) {
	s := newVaultSettings(t)
	const name, secret = "github", "ghp_legacy_plaintext"
	if err := s.Store.UpsertMCPServer(t.Context(), db.MCPServerRow{
		Name: name, Transport: "http", BaseURL: "https://mcp.example.com", AuthToken: secret, Enabled: true,
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	rows, _ := s.Store.ListMCPServers(t.Context())
	if got := resolveMCPAuthToken(t.Context(), s, rows[0]); got != secret {
		t.Fatalf("resolve = %q; want legacy token %q", got, secret)
	}

	// Column cleared in the DB.
	rows, _ = s.Store.ListMCPServers(t.Context())
	if rows[0].AuthToken != "" {
		t.Errorf("plaintext token still in mcp_servers after migration: %q", rows[0].AuthToken)
	}
	// And now resolvable from the vault.
	if k, ok, err := s.Svc.GetKey(t.Context(), prefs.ScopeGlobal, mcpAuthKeyProvider(name)); err != nil || !ok || k != secret {
		t.Errorf("vault lookup = %q ok=%v err=%v; want %q", k, ok, err, secret)
	}
	if got := resolveMCPAuthToken(t.Context(), s, rows[0]); got != secret {
		t.Errorf("second resolve (from vault) = %q; want %q", got, secret)
	}
}

// Without a vault the legacy plaintext is returned unchanged — degraded
// but functional, never blocking launch.
func TestResolveMCPAuthToken_NoVaultFallsBackToPlaintext(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := engine.NewSettings(t.Context(), store, nil, "")
	row := db.MCPServerRow{Name: "x", Transport: "http", BaseURL: "https://h", AuthToken: "plain", Enabled: true}
	if got := resolveMCPAuthToken(t.Context(), s, row); got != "plain" {
		t.Errorf("no-vault resolve = %q; want plaintext passthrough", got)
	}
}
