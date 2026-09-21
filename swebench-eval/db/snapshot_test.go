package db_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/zarldev/zarlmono/swebench-eval/db"
)

func TestManifestMigrationKeepsHistoricalInputsUnknown(t *testing.T) {
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
	if _, err := provider.UpTo(t.Context(), 6); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO eval_runs (id, started_at, dataset_name, language_filter, sample_size, drivers, task_timeout_ms, notes) VALUES ('old', 1, 'dataset', '', 3, 'zarlcode', 1, '')`); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.ReadRunSnapshot(t.Context(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Run.ManifestJSON != "" || snapshot.Run.EndedAt != nil || len(snapshot.Results) != 0 {
		t.Fatalf("invented historical data: %+v", snapshot)
	}
}

func TestRunSnapshotPreservesManifestAndOrderedOutcomes(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ReadRunSnapshot(t.Context(), "absent"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	const manifest = `{"format_version":1,"tasks":[{"instance_id":"b"},{"instance_id":"a"}]}`
	if err := store.InsertRun(t.Context(), db.RunRecord{ID: "run", StartedAt: time.Unix(1, 0), ManifestJSON: manifest}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []db.ResultRecord{
		{RunID: "run", InstanceID: "b", DriverName: "zarlcode", Resolved: new(false), Error: "task timeout"},
		{RunID: "run", InstanceID: "a", DriverName: "zarlcode-judge", EvaluatorError: "verifier unavailable"},
		{RunID: "run", InstanceID: "a", DriverName: "zarlcode", Resolved: new(true)},
	} {
		if err := store.InsertResult(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"running", "failed"} {
		if err := store.AppendScoreAttempt(t.Context(), db.ScoreAttemptEvent{RunID: "run", AttemptID: "attempt", Status: status, RecordedAt: time.Unix(2, 0), Payload: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.ReadRunSnapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Run.ManifestJSON != manifest || snapshot.Run.EndedAt != nil || len(snapshot.Results) != 3 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	a, judge, b := snapshot.Results[0], snapshot.Results[1], snapshot.Results[2]
	if a.InstanceID != "a" || a.DriverName != "zarlcode" || a.Resolved == nil || !*a.Resolved || judge.Resolved != nil || b.Resolved == nil || *b.Resolved {
		t.Fatalf("ordered nullable verdicts: %+v", snapshot.Results)
	}
	if len(snapshot.ScoreAttempts) != 2 || snapshot.ScoreAttempts[0].Status != "running" || snapshot.ScoreAttempts[1].Status != "failed" {
		t.Fatalf("scoring lifecycle: %+v", snapshot.ScoreAttempts)
	}
	runs, err := store.ListRecentRuns(t.Context(), 1)
	if err != nil || len(runs) != 1 || runs[0].ManifestJSON != manifest {
		t.Fatalf("manifest missing from run listing: %+v %v", runs, err)
	}
}

func TestRunSnapshotDoesNotTearConcurrentScoringCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eval.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InsertRun(t.Context(), db.RunRecord{ID: "run", StartedAt: time.Unix(1, 0), ScoreStatus: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertResult(t.Context(), db.ResultRecord{RunID: "run", InstanceID: "task", DriverName: "driver", Resolved: new(false)}); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var writerErr error
	go func() {
		defer close(done)
		for i := range 40 {
			if err := writeScoringPair(ctx, writer, i%2 == 0); err != nil {
				writerErr = err
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-done
	}()
	for range 80 {
		snapshot, err := store.ReadRunSnapshot(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Results) != 1 || snapshot.Results[0].Resolved == nil {
			t.Fatalf("missing committed result: %+v", snapshot)
		}
		if (snapshot.Run.ScoreStatus == "completed") != *snapshot.Results[0].Resolved {
			t.Fatalf("mixed different committed states: %+v", snapshot)
		}
	}
	<-done
	if writerErr != nil {
		t.Fatalf("scoring writer: %v", writerErr)
	}
}

func writeScoringPair(ctx context.Context, database *sql.DB, resolved bool) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status := "failed"
	if resolved {
		status = "completed"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE eval_runs SET score_status = ? WHERE id = 'run'`, status); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE eval_results SET resolved = ? WHERE run_id = 'run'`, resolved); err != nil {
		return err
	}
	return tx.Commit()
}
