package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type scanner interface {
	Scan(...any) error
}

// RunSnapshot owns a consistent read of one run, its sorted task results, and
// scoring events in insertion order. An unfinished run remains unfinished.
type RunSnapshot struct {
	Run           RunRecord
	Results       []ResultRecord
	ScoreAttempts []ScoreAttemptEvent
}

// ReadRunSnapshot reads all exported records in one transaction so concurrent
// scoring cannot mix different committed states. A missing run wraps ErrNotFound.
func (s *Store) ReadRunSnapshot(ctx context.Context, runID string) (RunSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RunSnapshot{}, fmt.Errorf("begin run snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	run, err := scanRun(tx.QueryRowContext(ctx, `
		SELECT id, started_at, ended_at, dataset_name, language_filter,
		       sample_size, drivers, task_timeout_ms, notes, score_status, score_error, manifest_json
		FROM eval_runs WHERE id = ?`, runID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunSnapshot{}, fmt.Errorf("read run: %w", ErrNotFound)
		}
		return RunSnapshot{}, fmt.Errorf("read run: %w", err)
	}
	results, err := listResultsForRun(ctx, tx, runID)
	if err != nil {
		return RunSnapshot{}, fmt.Errorf("read run results: %w", err)
	}
	attempts, err := listScoreAttempts(ctx, tx, runID)
	if err != nil {
		return RunSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunSnapshot{}, fmt.Errorf("finish run snapshot: %w", err)
	}
	return RunSnapshot{Run: run, Results: results, ScoreAttempts: attempts}, nil
}

func scanRun(row scanner) (RunRecord, error) {
	var r RunRecord
	var startedAt int64
	var endedAt sql.NullInt64
	if err := row.Scan(
		&r.ID, &startedAt, &endedAt, &r.DatasetName, &r.LanguageFilter,
		&r.SampleSize, &r.Drivers, &r.TaskTimeoutMs, &r.Notes, &r.ScoreStatus, &r.ScoreError, &r.ManifestJSON,
	); err != nil {
		return RunRecord{}, err
	}
	r.StartedAt = time.Unix(startedAt, 0)
	if endedAt.Valid {
		t := time.Unix(endedAt.Int64, 0)
		r.EndedAt = &t
	}
	return r, nil
}
