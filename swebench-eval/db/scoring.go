package db

import (
	"context"
	"database/sql"
	"fmt"
)

// UpdateRunScore records scoring progress independently of per-task verdicts.
func (s *Store) UpdateRunScore(ctx context.Context, runID, status, diagnostic string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE eval_runs SET score_status = ?, score_error = ? WHERE id = ?`, status, diagnostic, runID)
	if err != nil {
		return fmt.Errorf("update scoring %q: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated scoring rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update scoring %q: %w", runID, sql.ErrNoRows)
	}
	return nil
}
