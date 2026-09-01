-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1));
ALTER TABLE sessions ADD COLUMN pinned_at INTEGER;
CREATE INDEX idx_sessions_workspace_pinned_updated
    ON sessions(workspace, pinned DESC, pinned_at DESC, updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_workspace_pinned_updated;
ALTER TABLE sessions DROP COLUMN pinned_at;
ALTER TABLE sessions DROP COLUMN pinned;
-- +goose StatementEnd
