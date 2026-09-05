package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestMigrationGapFrom22ToLatestAndBack(t *testing.T) {
	t.Parallel()

	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration-gap.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	provider, err := goose.NewProvider(goose.DialectSQLite3, d, os.DirFS("migrations"))
	if err != nil {
		t.Fatalf("new migration provider: %v", err)
	}
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 22); err != nil {
		t.Fatalf("migrate to 22: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO sessions (id, workspace, history_json, created_at, updated_at)
		VALUES ('gap', '/workspace', '[{"role":"user","content":"preserved"}]', 1, 1)`); err != nil {
		t.Fatalf("seed version 22 session: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate across gap to latest: %v", err)
	}
	assertMigrationVersion(t, provider, 27)
	assertTableExists(t, d, "session_transcripts", true)
	assertTableExists(t, d, "session_transcript_entries", true)
	assertSessionColumnExists(t, d, "context_json", true)
	assertSessionColumnExists(t, d, "history_json", false)

	for i := range 3 {
		if _, err := provider.Down(ctx); err != nil {
			t.Fatalf("down migration %d: %v", i+1, err)
		}
	}
	assertMigrationVersion(t, provider, 22)
	assertTableExists(t, d, "session_transcripts", false)
	assertTableExists(t, d, "session_transcript_entries", false)
	assertSessionColumnExists(t, d, "context_json", false)
	assertSessionColumnExists(t, d, "history_json", true)

	var restored string
	if err := d.QueryRowContext(ctx, "SELECT history_json FROM sessions WHERE id = 'gap'").Scan(&restored); err != nil {
		t.Fatalf("read restored context JSON: %v", err)
	}
	if want := `[{"role":"user","content":"preserved"}]`; restored != want {
		t.Fatalf("restored context JSON = %q, want %q", restored, want)
	}
}

func assertMigrationVersion(t *testing.T, provider *goose.Provider, want int64) {
	t.Helper()

	got, err := provider.GetDBVersion(t.Context())
	if err != nil {
		t.Fatalf("get migration version: %v", err)
	}
	if got != want {
		t.Fatalf("migration version = %d, want %d", got, want)
	}
}

func assertTableExists(t *testing.T, d *sql.DB, table string, want bool) {
	t.Helper()

	var count int
	if err := d.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
		t.Fatalf("query table %q: %v", table, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("table %q exists = %t, want %t", table, got, want)
	}
}

func assertSessionColumnExists(t *testing.T, d *sql.DB, column string, want bool) {
	t.Helper()

	const table = "sessions"
	rows, err := d.QueryContext(t.Context(), "PRAGMA table_info(sessions)")
	if err != nil {
		t.Fatalf("query columns for %q: %v", table, err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan columns for %q: %v", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %q: %v", table, err)
	}
	if found != want {
		t.Fatalf("column %q.%q exists = %t, want %t", table, column, found, want)
	}
}
