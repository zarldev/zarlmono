-- +goose Up
-- +goose StatementBegin

CREATE TABLE mcp_servers (
    name       TEXT NOT NULL PRIMARY KEY,
    transport  TEXT NOT NULL,              -- 'stdio' | 'http'
    command    TEXT NOT NULL DEFAULT '',   -- stdio: binary path
    args       TEXT NOT NULL DEFAULT '[]', -- stdio: JSON array of args
    env        TEXT NOT NULL DEFAULT '{}', -- stdio: JSON object of env vars
    base_url   TEXT NOT NULL DEFAULT '',   -- http: server URL
    auth_token TEXT NOT NULL DEFAULT '',   -- http: bearer token
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mcp_servers;
-- +goose StatementEnd
