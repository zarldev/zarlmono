-- +goose Up
-- Unpublished two-step captures are durable GC roots until publication or
-- explicit release. No expiry: elapsed time cannot prove a capture is unused.
CREATE TABLE session_history_pins (
    id TEXT PRIMARY KEY,
    transcript_head TEXT NOT NULL,
    replay_head TEXT NOT NULL,
    state_id TEXT NOT NULL REFERENCES session_history_values(id)
);

-- +goose Down
DROP TABLE session_history_pins;
