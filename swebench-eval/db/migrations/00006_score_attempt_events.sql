-- +goose Up
-- Append-only scoring invocation events. No historical outcomes are inferred.
CREATE TABLE eval_score_attempt_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    attempt_id TEXT NOT NULL,
    status TEXT NOT NULL,
    recorded_at INTEGER NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX eval_score_attempt_events_run ON eval_score_attempt_events(run_id, sequence);

-- +goose Down
-- Destructive downgrade: removes the scoring attempt trail.
DROP TABLE eval_score_attempt_events;
