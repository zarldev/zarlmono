-- +goose Up
-- +goose StatementBegin
CREATE TABLE tool_outputs (
    id           INTEGER PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL,
    tool_name    TEXT NOT NULL,
    args_json    TEXT NOT NULL DEFAULT 'null',
    output       TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    UNIQUE (session_id, tool_call_id)
);
CREATE INDEX idx_tool_outputs_session ON tool_outputs(session_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tool_outputs_session;
DROP TABLE IF EXISTS tool_outputs;
-- +goose StatementEnd
