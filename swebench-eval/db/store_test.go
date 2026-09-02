package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/zarldev/zarlmono/swebench-eval/db"
	_ "modernc.org/sqlite"
)

func TestVerifyTelemetryMigratesExistingRows(t *testing.T) {
	t.Parallel()

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
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO eval_runs (id, started_at, dataset_name, language_filter, sample_size, drivers, task_timeout_ms, notes)
		VALUES ('run', 1, 'dataset', 'go', 1, 'zarlcode', 1000, '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO eval_results (run_id, instance_id, driver_name, language, worktree_path, diff, duration_ms, iterations, tool_calls, tokens_in, tokens_out, terminal_reason, error, created_at)
		VALUES ('run', 'task', 'zarlcode', 'go', '/tmp/work', 'diff', 1, 1, 1, 1, 1, 'done', '', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var verified bool
	var attempts int
	var verdicts string
	if err := database.QueryRowContext(ctx, `SELECT verified, attempts, attempt_verdicts FROM eval_results WHERE run_id = 'run'`).Scan(&verified, &attempts, &verdicts); err != nil {
		t.Fatal(err)
	}
	if verified || attempts != 0 || verdicts != "" {
		t.Fatalf("historical defaults = (%t, %d, %q)", verified, attempts, verdicts)
	}
}

func TestListResultsRoundTripsVerifyTelemetry(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	if err := store.InsertRun(t.Context(), db.RunRecord{ID: "run", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertResult(t.Context(), db.ResultRecord{
		RunID: "run", InstanceID: "task", DriverName: "zarlcode", CreatedAt: now,
		Verified: true, Attempts: 2, AttemptVerdicts: `[{"attempt":1,"resolved":false},{"attempt":2,"resolved":true}]`,
	}); err != nil {
		t.Fatal(err)
	}
	results, err := store.ListResultsForRun(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Verified || results[0].Attempts != 2 || results[0].AttemptVerdicts == "" {
		t.Fatalf("results = %#v", results)
	}
}
