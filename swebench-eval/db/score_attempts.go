package db

import (
	"context"
	"fmt"
	"time"
)

// ScoreAttemptEvent is an append-only invocation lifecycle record. Payload holds
// the caller's serialized attempt identity, configuration, timing and summary.
// A running event without a terminal event denotes an unfinished invocation.
type ScoreAttemptEvent struct {
	Sequence   int64
	RunID      string
	AttemptID  string
	Status     string
	RecordedAt time.Time
	Payload    string
}

// AppendScoreAttempt preserves a lifecycle event without replacing prior attempts.
func (s *Store) AppendScoreAttempt(ctx context.Context, event ScoreAttemptEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO eval_score_attempt_events (run_id, attempt_id, status, recorded_at, payload) VALUES (?, ?, ?, ?, ?)`, event.RunID, event.AttemptID, event.Status, event.RecordedAt.UnixNano(), event.Payload)
	if err != nil {
		return fmt.Errorf("append scoring attempt: %w", err)
	}
	return nil
}

// ListScoreAttempts returns all invocation events in durable insertion order.
func (s *Store) ListScoreAttempts(ctx context.Context, runID string) ([]ScoreAttemptEvent, error) {
	return listScoreAttempts(ctx, s.db, runID)
}

func listScoreAttempts(ctx context.Context, q queryer, runID string) ([]ScoreAttemptEvent, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence, run_id, attempt_id, status, recorded_at, payload FROM eval_score_attempt_events WHERE run_id = ? ORDER BY sequence`, runID)
	if err != nil {
		return nil, fmt.Errorf("list scoring attempts: %w", err)
	}
	defer rows.Close()
	var events []ScoreAttemptEvent
	for rows.Next() {
		var event ScoreAttemptEvent
		var recorded int64
		if err := rows.Scan(&event.Sequence, &event.RunID, &event.AttemptID, &event.Status, &recorded, &event.Payload); err != nil {
			return nil, fmt.Errorf("scan scoring attempt: %w", err)
		}
		event.RecordedAt = time.Unix(0, recorded)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scoring attempts: %w", err)
	}
	return events, nil
}
