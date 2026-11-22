package db_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestMigrationHeadlessChildAccountingPreservesVersion41(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration41.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	provider, err := migrations.NewProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 41); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), `INSERT INTO headless_runs(id,workspace,prompt,started_at,provider,model) VALUES ('legacy','/workspace','old',1,'provider','model');
 INSERT INTO headless_provider_accounting(run_id,pursue_attempt,provider_ordinal,version,payload_json,checksum) VALUES ('legacy',1,1,1,X'7b7d','old-main');
 INSERT INTO headless_auxiliary_provider_accounting(run_id,invocation_id,version,payload_json,checksum) VALUES ('legacy','aux',1,X'7b7d','old-aux');`); err != nil {
		t.Fatal(err)
	}
	tables := []string{"headless_runs", "headless_attempts", "headless_provider_accounting", "headless_auxiliary_provider_accounting", "session_provider_accounting", "session_child_provider_accounting", "session_auxiliary_provider_accounting"}
	before := make(map[string][][]any, len(tables))
	for _, table := range tables {
		before[table] = childMigrationRows(t, raw, table)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, table := range tables {
		if got := childMigrationRows(t, s.DB(), table); !reflect.DeepEqual(before[table], got) {
			t.Fatalf("changed %s", table)
		}
	}
	if rows, err := s.ListHeadlessChildProviderAccounting(t.Context(), "legacy", 0, 256); err != nil || len(rows) != 0 {
		t.Fatalf("fabricated children: %+v %v", rows, err)
	}
	provider, err = migrations.NewProvider(s.DB())
	if err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(t.Context()); err != nil || version != 44 {
		t.Fatalf("version %d: %v", version, err)
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(t.Context()); err != nil || version != 42 {
		t.Fatalf("down version %d: %v", version, err)
	}
	for _, table := range tables {
		if got := childMigrationRows(t, s.DB(), table); !reflect.DeepEqual(before[table], got) {
			t.Fatalf("down changed %s", table)
		}
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if got := childMigrationRows(t, s.DB(), table); !reflect.DeepEqual(before[table], got) {
			t.Fatalf("up changed %s", table)
		}
	}
}
