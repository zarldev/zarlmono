-- +goose Up
-- New capture only: existing latest-output rows are not rewritten or backfilled.
CREATE TABLE tool_output_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    execution_id TEXT NOT NULL,
    parent_tool_call_id TEXT NOT NULL,
    args_json TEXT NOT NULL,
    parameters_json TEXT NOT NULL,
    output TEXT NOT NULL,
    parts_json TEXT NOT NULL,
    effects_json TEXT NOT NULL,
    success INTEGER NOT NULL CHECK (success IN (0, 1)),
    error TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_tool_output_history_session ON tool_output_history(session_id, id);

-- +goose Down
-- Explicit downgrade discards only history captured after this migration.
DROP TABLE tool_output_history;
