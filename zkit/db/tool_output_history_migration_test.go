package db_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestMigrationToolHistoryClassification(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{"legacy", "canonical", "partial"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state.db")
			d, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			provider, err := migrations.NewProvider(d)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if _, err := provider.UpTo(ctx, 29); err != nil {
				t.Fatal(err)
			}
			if shape == "canonical" {
				if _, err := provider.UpTo(ctx, 30); err != nil {
					t.Fatal(err)
				}
			} else {
				// Exact observed 12-column version-30 shape; do not derive this
				// fixture from the corrected migration or it cannot catch drift.
				if _, err := d.ExecContext(ctx, `CREATE TABLE tool_output_history (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
					tool_call_id TEXT NOT NULL, tool_name TEXT NOT NULL,
					execution_id TEXT NOT NULL, parent_tool_call_id TEXT NOT NULL,
					args_json TEXT NOT NULL, parameters_json TEXT NOT NULL,
					output TEXT NOT NULL, parts_json TEXT NOT NULL,
					effects_json TEXT NOT NULL, created_at INTEGER NOT NULL);
					CREATE INDEX idx_tool_output_history_session ON tool_output_history(session_id, id);
					INSERT INTO goose_db_version (version_id, is_applied) VALUES (30, 1)`); err != nil {
					t.Fatal(err)
				}
				if shape == "partial" {
					if _, err := d.ExecContext(ctx, "ALTER TABLE tool_output_history ADD COLUMN success INTEGER NOT NULL DEFAULT 0 CHECK (success IN (0, 1))"); err != nil {
						t.Fatal(err)
					}
				}
			}
			assertMigrationVersion(t, provider, 30)
			if _, err := d.ExecContext(ctx, `INSERT INTO sessions (id, workspace, created_at, updated_at) VALUES ('history', '/workspace', 1, 1)`); err != nil {
				t.Fatal(err)
			}
			insert := `INSERT INTO tool_output_history (id, session_id, tool_call_id, tool_name, execution_id, parent_tool_call_id, args_json, parameters_json, output, parts_json, effects_json, created_at`
			values := ` VALUES (41, 'history', 'old-call', 'bash', 'old-exec', 'parent', '{"raw":1}', '{"decoded":1}', 'old-output', '[]', '[]', 1`
			if shape == "canonical" {
				insert += ", success, error, kind"
				values += ", 0, 'old-error', 'transient'"
			}
			if _, err := d.ExecContext(ctx, insert+")"+values+")"); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			// Exercise the actual production opener, not a test-only repair.
			store, err := db.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			assertToolHistoryColumns(t, store.DB())
			provider, err = migrations.NewProvider(store.DB())
			if err != nil {
				t.Fatal(err)
			}
			assertMigrationVersion(t, provider, 34)
			history, err := store.ListToolOutputHistory(ctx, "history")
			if err != nil || len(history) != 1 {
				t.Fatalf("read preserved history: count=%d, err=%v", len(history), err)
			}
			old := history[0]
			if old.ID != 41 || old.Output != "old-output" || old.ExecutionID != "old-exec" || old.ParentToolCallID != "parent" || old.ArgsJSON != `{"raw":1}` || old.ParametersJSON != `{"decoded":1}` || old.PartsJSON != "[]" || old.EffectsJSON != "[]" || old.CreatedAt.Unix() != 1 {
				t.Fatal("migration changed legacy history")
			}
			if shape == "canonical" {
				if old.Success || old.Error != "old-error" || old.Kind != tools.Kinds.TRANSIENT {
					t.Fatal("migration changed existing classification")
				}
			} else if old.Success || old.Error != "" || old.Kind.String() != "unknown" {
				t.Fatal("migration invented legacy classification")
			}
			if err := store.CaptureToolOutput(ctx, "history", db.ToolOutputHistory{
				ToolOutputRecord: db.ToolOutputRecord{ToolCallID: "new-call", ToolName: "bash", Output: "new-output"},
				ExecutionID:      "new-exec", Success: true,
			}); err != nil {
				t.Fatalf("capture after upgrade: %v", err)
			}
			history, err = store.ListToolOutputHistory(ctx, "history")
			if err != nil || len(history) != 2 || !reflect.DeepEqual(old, history[0]) || history[1].ID <= old.ID || !history[1].Success || history[1].Output != "new-output" {
				t.Fatalf("history after capture mismatch: %v", err)
			}
			if _, err := provider.DownTo(ctx, 30); err != nil {
				t.Fatal(err)
			}
			assertMigrationVersion(t, provider, 30)
			assertToolHistoryColumns(t, store.DB())
			if _, err := provider.Up(ctx); err != nil {
				t.Fatal(err)
			}
			after, err := store.ListToolOutputHistory(ctx, "history")
			if err != nil || !reflect.DeepEqual(history, after) {
				t.Fatalf("down/up changed captured history: %v", err)
			}
			if _, err := provider.Up(ctx); err != nil {
				t.Fatalf("repeated up: %v", err)
			}
		})
	}
}

func TestMigrationToolHistoryMissingTableRefusesVersionAdvance(t *testing.T) {
	t.Parallel()
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	provider, err := migrations.NewProvider(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 29); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES (30, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err == nil {
		t.Fatal("marked unrepaired database current")
	}
	assertMigrationVersion(t, provider, 30)
}

func assertToolHistoryColumns(t *testing.T, d *sql.DB) {
	t.Helper()
	for _, name := range []string{"success", "error", "kind"} {
		var count int
		if err := d.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('tool_output_history') WHERE name = ? AND "notnull" = 1`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("missing NOT NULL classification column %s", name)
		}
	}
}
