package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestMCPServerAuthIntentRoundTrip(t *testing.T) {
	t.Parallel()
	store := openTempStore(t)
	want := db.MCPServerRow{
		Name:         "private",
		Transport:    "http",
		BaseURL:      "https://mcp.example.com",
		AuthRequired: true,
		Enabled:      true,
	}
	if err := store.UpsertMCPServer(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListMCPServers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].AuthRequired || rows[0].AuthToken != "" {
		t.Fatalf("MCP server = %#v; want required auth without plaintext token", rows)
	}
}

func TestMCPAuthIntentMigrationPreservesRequiredState(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "mcp-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO mcp_servers (name, transport, base_url, auth_token, enabled, created_at, updated_at)
		VALUES ('plaintext', 'http', 'https://one.example', 'do-not-read', 1, 1, 1),
		       ('encrypted', 'http', 'https://two.example', '', 1, 1, 1),
		       ('public', 'http', 'https://three.example', '', 1, 1, 1);
		INSERT INTO api_keys (workspace, provider, ciphertext, nonce, key_version, updated_at, storage)
		VALUES ('', 'mcp:encrypted', X'01', X'02', 2, 1, 'vault')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := database.QueryContext(ctx, `SELECT name, auth_required, auth_token FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]struct {
		required int
		token    string
	}{}
	for rows.Next() {
		var name, token string
		var required int
		if err := rows.Scan(&name, &required, &token); err != nil {
			t.Fatal(err)
		}
		got[name] = struct {
			required int
			token    string
		}{required: required, token: token}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got["encrypted"].required != 1 || got["plaintext"].required != 1 || got["public"].required != 0 {
		t.Fatalf("auth intent = %#v", got)
	}
	if got["plaintext"].token != "do-not-read" {
		t.Fatal("migration changed obsolete plaintext token bytes")
	}
}
