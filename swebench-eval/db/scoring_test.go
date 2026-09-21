package db_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/zarldev/zarlmono/swebench-eval/db"
)

func TestScoringMigrationAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eval.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 4); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO eval_runs (id, started_at, dataset_name, language_filter, sample_size, drivers, task_timeout_ms, notes) VALUES ('old', 1, 'dataset', '', 1, 'driver', 1, '')`); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ListRecentRuns(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ScoreStatus != "not_recorded" || runs[0].ScoreError != "" {
		t.Fatalf("historical state: %+v", runs)
	}
	if err := store.InsertRun(t.Context(), db.RunRecord{ID: "new", StartedAt: time.Now(), ScoreStatus: "pending"}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "unavailable", "failed", "cancelled", "completed", "not_requested"} {
		if err := store.UpdateRunScore(t.Context(), "new", status, "diagnostic"); err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListRecentRuns(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].ScoreStatus != status || rows[0].ScoreError != "diagnostic" {
			t.Fatalf("round trip: %+v", rows)
		}
	}
}

func TestScoreUpdatesRequirePersistedRows(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateRunScore(t.Context(), "missing", "completed", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing run: %v", err)
	}
	if err := store.UpdateResolved(t.Context(), "missing", "task", "driver", new(true), ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing result: %v", err)
	}
}
