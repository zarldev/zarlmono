// Package db persists swebench-eval results so runs can be compared
// over time. Sits in a separate sqlite file (~/.zarlcode/swebench-eval.db)
// rather than co-locating with zarlcode's state.db — the eval tool is
// downstream of zarlcode and shouldn't co-own its schema.
//
// Two tables:
//
//	eval_runs    — one row per invocation of `swebench-eval` (params,
//	               start, end, summary).
//	eval_results — one row per (run, task, driver) — what the harness
//	               produced and (eventually) what the scorer verdict
//	               was.
//
// The schema is hand-rolled, no sqlc — five queries total; the
// generator overhead isn't earning anything here. If the surface
// grows past ~a dozen queries we'll graduate.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a Get does not match a row.
var ErrNotFound = errors.New("swebench-eval/db: not found")

// DefaultPath returns ~/.zarlcode/swebench-eval.db, the canonical
// location. Created on first Open if the directory doesn't exist.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home: %w", err)
	}
	return filepath.Join(home, ".zarlcode", "swebench-eval.db"), nil
}

// Store wraps the connection and exposes typed methods. Goroutine-
// safe by virtue of database/sql.DB.
type Store struct {
	db *sql.DB
}

// Open connects to the sqlite file at path, creating the directory
// + running migrations as needed. Path "" resolves to DefaultPath().
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the connection.
func (s *Store) Close() error { return s.db.Close() }

// migrate runs every migration in migrations/*.sql against db, in
// lexicographic order. Goose is used for compatibility with the rest
// of the monorepo's migration tooling — same format, same ordering
// rules, so an operator who knows zarlcode's db knows this one.
func migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("dialect: %w", err)
	}
	// Goose expects a directory of migration files; we have them in
	// embed.FS so we pass "migrations" as the directory.
	return goose.UpContext(ctx, db, "migrations")
}

// --- types ---

// RunRecord is one eval invocation.
type RunRecord struct {
	ID             string
	StartedAt      time.Time
	EndedAt        *time.Time
	DatasetName    string
	LanguageFilter string
	SampleSize     int
	Drivers        string
	TaskTimeoutMs  int64
	Notes          string
}

// ResultRecord is one (task, driver) outcome within a run.
// Resolved is nullable: nil = scoring not run (or skipped this record).
//
// Provider + Model identify the LLM the driver invoked under the hood
// — the dimension that turns "harness A beat harness B" into
// "harness A on model X beat harness B on model Y". Empty = unknown
// (driver didn't surface it, or the row predates the column).
type ResultRecord struct {
	RunID          string
	InstanceID     string
	DriverName     string
	Language       string
	WorktreePath   string
	Diff           string
	DurationMs     int64
	Iterations     int
	ToolCalls      int
	TokensIn       int64
	TokensOut      int64
	TerminalReason string
	Error          string
	Resolved       *bool
	EvaluatorError string
	Provider       string
	Model          string
	// GuardrailRejections is the per-guardrail rejection-count map
	// serialized as a JSON object ("" when the driver surfaced none).
	GuardrailRejections string
	// Verified / Attempts / AttemptVerdicts are the verify-loop telemetry:
	// goal-confirmed success, agent attempts consumed, and the JSON array
	// of per-attempt verifier verdicts ("" for single-shot runs).
	Verified        bool
	Attempts        int
	AttemptVerdicts string
	CreatedAt       time.Time
}

// --- inserts ---

// InsertRun records the start of an eval invocation. Caller calls
// FinishRun once the run completes.
func (s *Store) InsertRun(ctx context.Context, r RunRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO eval_runs (
		  id, started_at, dataset_name, language_filter,
		  sample_size, drivers, task_timeout_ms, notes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		r.ID, r.StartedAt.Unix(), r.DatasetName, r.LanguageFilter,
		r.SampleSize, r.Drivers, r.TaskTimeoutMs, r.Notes,
	)
	if err != nil {
		return fmt.Errorf("insert run %q: %w", r.ID, err)
	}
	return nil
}

// FinishRun stamps ended_at on a previously-inserted run.
func (s *Store) FinishRun(ctx context.Context, runID string, endedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE eval_runs SET ended_at = ? WHERE id = ?`,
		endedAt.Unix(), runID,
	)
	if err != nil {
		return fmt.Errorf("finish run %q: %w", runID, err)
	}
	return nil
}

// InsertResult writes one (task, driver) outcome.
func (s *Store) InsertResult(ctx context.Context, r ResultRecord) error {
	resolved := nullableBool(r.Resolved)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO eval_results (
		  run_id, instance_id, driver_name, language, worktree_path,
		  diff, duration_ms, iterations, tool_calls,
		  tokens_in, tokens_out, terminal_reason, error,
		  resolved, evaluator_error, provider, model,
		  guardrail_rejections, verified, attempts, attempt_verdicts,
		  created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		r.RunID, r.InstanceID, r.DriverName, r.Language, r.WorktreePath,
		r.Diff, r.DurationMs, r.Iterations, r.ToolCalls,
		r.TokensIn, r.TokensOut, r.TerminalReason, r.Error,
		resolved, r.EvaluatorError, r.Provider, r.Model,
		r.GuardrailRejections, r.Verified, r.Attempts, r.AttemptVerdicts,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert result %q/%q/%q: %w", r.RunID, r.InstanceID, r.DriverName, err)
	}
	return nil
}

// BackfillResultProviderModel sets provider+model on all rows
// matching (run_id, driver_name) that currently have empty values.
// Used to retroactively complete rows the in-flight run wrote
// before the columns existed.
func (s *Store) BackfillResultProviderModel(ctx context.Context, runID, driverName, provider, model string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE eval_results
		SET provider = ?, model = ?
		WHERE run_id = ? AND driver_name = ? AND provider = '' AND model = ''
	`, provider, model, runID, driverName)
	if err != nil {
		return 0, fmt.Errorf("backfill provider/model %q/%q: %w", runID, driverName, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// UpdateResolved patches an existing result row with the scorer's
// verdict. Called after Score completes.
func (s *Store) UpdateResolved(ctx context.Context, runID, instanceID, driverName string, resolved *bool, evalErr string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE eval_results
		SET resolved = ?, evaluator_error = ?
		WHERE run_id = ? AND instance_id = ? AND driver_name = ?
	`,
		nullableBool(resolved), evalErr,
		runID, instanceID, driverName,
	)
	if err != nil {
		return fmt.Errorf("update resolved %q/%q/%q: %w", runID, instanceID, driverName, err)
	}
	return nil
}

// --- reads ---

// ListRecentRuns returns the most recent N runs, newest first.
// Useful for "show me what I've evaluated lately" CLI output.
func (s *Store) ListRecentRuns(ctx context.Context, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, started_at, ended_at, dataset_name, language_filter,
		       sample_size, drivers, task_timeout_ms, notes
		FROM eval_runs
		ORDER BY started_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RunRecord
	for rows.Next() {
		var r RunRecord
		var startedAt int64
		var endedAt sql.NullInt64
		if err := rows.Scan(
			&r.ID, &startedAt, &endedAt, &r.DatasetName, &r.LanguageFilter,
			&r.SampleSize, &r.Drivers, &r.TaskTimeoutMs, &r.Notes,
		); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(startedAt, 0)
		if endedAt.Valid {
			t := time.Unix(endedAt.Int64, 0)
			r.EndedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListResultsForRun returns every result row for one run, ordered by
// instance_id for stable diffing against past runs.
func (s *Store) ListResultsForRun(ctx context.Context, runID string) ([]ResultRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, instance_id, driver_name, language, worktree_path,
		       diff, duration_ms, iterations, tool_calls,
		       tokens_in, tokens_out, terminal_reason, error,
		       resolved, evaluator_error, provider, model, created_at
		FROM eval_results
		WHERE run_id = ?
		ORDER BY instance_id, driver_name
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResultRecord
	for rows.Next() {
		var r ResultRecord
		var createdAt int64
		var resolved sql.NullBool
		if err := rows.Scan(
			&r.RunID, &r.InstanceID, &r.DriverName, &r.Language, &r.WorktreePath,
			&r.Diff, &r.DurationMs, &r.Iterations, &r.ToolCalls,
			&r.TokensIn, &r.TokensOut, &r.TerminalReason, &r.Error,
			&resolved, &r.EvaluatorError, &r.Provider, &r.Model, &createdAt,
		); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(createdAt, 0)
		if resolved.Valid {
			b := resolved.Bool
			r.Resolved = &b
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- helpers ---

func nullableBool(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}
