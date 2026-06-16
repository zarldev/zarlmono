-- +goose Up
-- +goose StatementBegin

-- sessions: one row per saved conversation.
--
-- workspace is the absolute workspace path the session was created in;
-- sessions are scoped by workspace for the "resume" UX, but the global
-- table makes cross-workspace recall and search trivial.
--
-- history_json / pending_json / last_usage_json store the runtime
-- payloads as JSON blobs. We'll split history into a turns table in a
-- later migration once we want full-text search; the JSON blob is the
-- pragmatic shape today.
CREATE TABLE sessions (
    id               TEXT    PRIMARY KEY,
    workspace        TEXT    NOT NULL,
    label            TEXT    NOT NULL DEFAULT '',
    agent_name       TEXT    NOT NULL DEFAULT '',
    provider         TEXT    NOT NULL DEFAULT '',
    model            TEXT    NOT NULL DEFAULT '',
    history_json     TEXT    NOT NULL DEFAULT '[]',
    pending_json     TEXT    NOT NULL DEFAULT '[]',
    last_usage_json  TEXT    NOT NULL DEFAULT 'null',
    -- diff_bodies_json is a map[path]unified-diff-body captured from
    -- the file audit. Re-hydrated on -continue so the ✎ marker reappears
    -- in the Files dock and the diff viewer is populated. Counts and
    -- paths are derived from history_json (see session.restoreUI).
    diff_bodies_json TEXT    NOT NULL DEFAULT '{}',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

-- The hot query is "latest session for this workspace" — the index
-- covers both ORDER BY updated_at DESC and the workspace filter.
CREATE INDEX idx_sessions_workspace_updated_at
    ON sessions(workspace, updated_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_workspace_updated_at;
DROP TABLE IF EXISTS sessions;
-- +goose StatementEnd
