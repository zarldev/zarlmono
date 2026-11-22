package db_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestMigrationAuxiliaryAccountingPreservesVersion39(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "migration39.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	provider, err := migrations.NewProvider(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 39); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(t.Context(), `
		INSERT INTO sessions (id, workspace, context_json, last_usage_json, created_at, updated_at)
		VALUES ('legacy', '/workspace', '{"messages":["kept"]}', '{"CostUSD":17}', 1, 2);
		INSERT INTO session_history_batches (session_id, batch_id, checksum) VALUES ('legacy', 'receipt', 'original-checksum');
		INSERT INTO session_provider_accounting (session_id, request_batch_id, version, payload_json, checksum)
		VALUES ('legacy', 'receipt', 1, X'7b202276657273696f6e223a31207d', 'root-checksum');
		INSERT INTO session_child_provider_accounting (session_id, owner_request_batch_id, child_task_id, attempt, version, payload_json, checksum)
        VALUES ('legacy', 'receipt', 'child', 1, 1, X'7b7d', 'child-checksum');
        INSERT INTO headless_runs (id,workspace,prompt,started_at,provider,model) VALUES ('old-run','/workspace','original',1,'synthetic','model');
        INSERT INTO headless_attempts (run_id,attempt_number,prompt,decision_done,recorded_at) VALUES ('old-run',1,'original',0,2);
        INSERT INTO headless_provider_accounting (run_id, pursue_attempt, provider_ordinal, version, payload_json, checksum)
        VALUES ('old-run', 1, 1, 1, X'7b7d', 'headless-checksum');
        INSERT INTO api_keys (workspace, provider, ciphertext, nonce, key_version, updated_at, storage)
		VALUES ('/workspace', 'synthetic', X'00ff0102', X'010203', 1, 2, 'vault');
		INSERT INTO settings (workspace, key, value, updated_at) VALUES ('/workspace', 'provider', 'synthetic', 2);
		INSERT INTO session_transcripts (session_id, revision, checksum, format_version, created_at_ms, updated_at_ms)
		VALUES ('legacy', 0, 'transcript-checksum', 2, 1000, 2000);
	`); err != nil {
		t.Fatal(err)
	}
	// Compare every column and SQLite value type in each pre-existing table,
	// including synthetic encrypted bytes and deliberately noncanonical JSON.
	tables := []string{"sessions", "session_history_batches", "session_provider_accounting", "session_child_provider_accounting", "headless_runs", "headless_attempts", "headless_provider_accounting", "api_keys", "settings", "session_transcripts"}
	before := make(map[string][][]any)
	for _, table := range tables {
		before[table] = childMigrationRows(t, d, table)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], childMigrationRows(t, store.DB(), table)) {
			t.Fatalf("migration changed existing table %s", table)
		}
	}
	if rows, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "legacy", 0, 256); err != nil || len(rows) != 0 {
		t.Fatalf("historical auxiliary evidence fabricated: %v", err)
	}
	provider, err = migrations.NewProvider(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(t.Context())
	if err != nil || version != 44 {
		t.Fatalf("schema version = %d: %v", version, err)
	}
	rows, err := store.DB().QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after migration")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 39); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], childMigrationRows(t, store.DB(), table)) {
			t.Fatalf("down changed %s", table)
		}
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if records, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "legacy", 0, 256); err != nil || len(records) != 0 {
		t.Fatalf("re-up fabricated accounting: %v", err)
	}
}

func TestAuxiliaryAccountingSessionReceiptLifetimeAndFork(t *testing.T) {
	t.Parallel()
	store, checkpoint := historyStore(t)
	saveAccountingReceipt(t, store, "source", "owner")
	entry := auxiliaryAccountingEnvelope("owner")
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	original, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", entry.InvocationID)
	if err != nil {
		t.Fatal(err)
	}
	// A recovery/fork shares immutable history but never incurred accounting.
	if err := store.CreateCheckpointBranch(t.Context(), emptyCheckpointBranch(checkpoint)); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "child", 0, 256); err != nil || len(rows) != 0 {
		t.Fatalf("fork copied accounting: %v", err)
	}
	// Replacing the source's working/replay context with an earlier state does
	// not erase immutable evidence for attempts incurred later in that session.
	source, err := store.GetSession(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	source.ContextJSON = []byte(`[]`)
	if err := store.SaveSession(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpireSessionCheckpoints(t.Context(), time.Now().Add(100*365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GarbageCollectHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", entry.InvocationID)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatalf("receipt lifetime changed across fork/expiry/GC/source save: %v", err)
	}
}
