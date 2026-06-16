-- +goose Up
-- +goose StatementBegin

-- dynamic_tools: per-workspace registry of user-authored tools
-- (via /new_tool / register_tool). Previously persisted as
-- <workspace>/manifest.json — moved here so per-workspace state
-- has one consistent backup story (state.db) and stale entries
-- from old shell versions don't outlive whatever's in the binary
-- via a file we forgot existed.
--
-- spec_json is the full tools.ToolSpec (name, description,
-- parameters) as serialised by the manifest writer — opaque to the
-- store so the spec shape can evolve without schema churn.
-- binary_path is absolute on disk; the registry execs it on every
-- call.
--
-- workspace="" is the global slot — reserved (no caller writes
-- there today), kept for symmetry with settings/api_keys so
-- shared-tools-across-workspaces is a one-row change later.
CREATE TABLE dynamic_tools (
    workspace   TEXT    NOT NULL DEFAULT '',
    name        TEXT    NOT NULL,
    spec_json   TEXT    NOT NULL,
    binary_path TEXT    NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (workspace, name)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dynamic_tools;
-- +goose StatementEnd
