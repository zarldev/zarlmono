package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestRouteCachePolicyMigration44PreservesOldRows(t *testing.T) {
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	p, err := migrations.NewProvider(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(t.Context(), 43); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(t.Context(), "INSERT INTO llm_route_protocols (provider,alias,protocol) VALUES ('litellm','  Case/Ω  ','messages')"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		up      bool
		version int64
		present bool
	}{{true, 44, true}, {false, 43, false}, {true, 44, true}} {
		if step.up {
			_, err = p.Up(t.Context())
		} else {
			_, err = p.Down(t.Context())
		}
		if err != nil {
			t.Fatal(err)
		}
		version, err := p.GetDBVersion(t.Context())
		if err != nil || version != step.version {
			t.Fatalf("version %d %v", version, err)
		}
		var protocol string
		if err := d.QueryRowContext(t.Context(), "SELECT protocol FROM llm_route_protocols WHERE provider='litellm' AND alias='  Case/Ω  '").Scan(&protocol); err != nil || protocol != "messages" {
			t.Fatalf("old row: %q %v", protocol, err)
		}
		assertTableExists(t, d, "llm_route_cache_policies", step.present)
	}
	// Only the new table is removed by Down; old row and its type remain intact.
	var typ string
	if err := d.QueryRowContext(t.Context(), "SELECT typeof(protocol) FROM llm_route_protocols WHERE provider='litellm'").Scan(&typ); err != nil || typ != "text" {
		t.Fatalf("old row type: %q %v", typ, err)
	}
}
