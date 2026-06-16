-- +goose Up
-- +goose StatementBegin

-- settings: machine-managed key/value preferences. The shell stored
-- these in <workspace>/.zarlcode/settings.json before — the sqlite
-- version is global (one db at ~/.zarlcode/state.db) but keyed by
-- workspace so per-tree overrides still work. The empty string in
-- the workspace column is the "global default" row; lookups fall
-- back to it when there's no workspace-specific entry.
--
-- value is opaque JSON so callers can store strings, numbers, or
-- nested objects without schema churn. The set of recognised keys is
-- documented in zarlcode/db/store.go (theme, provider, model,
-- agent, compact_engine, compact_provider, compact_model, …).
CREATE TABLE settings (
    workspace  TEXT    NOT NULL DEFAULT '',
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (workspace, key)
);

-- api_keys: per-provider credentials, encrypted at rest. The cipher
-- is AES-GCM with the key derived from $ZARLCODE_KEY (or the env
-- fallback chain) — the bytes here are useless without the live
-- secret. Stored ciphertext + nonce + key_version so we can rotate
-- the master key in a later migration without losing existing rows.
--
-- workspace='' is the global default. When the shell looks up an
-- API key it tries the workspace-specific row first, then the global
-- row, then the provider's env-var fallback (e.g. ANTHROPIC_API_KEY).
CREATE TABLE api_keys (
    workspace   TEXT    NOT NULL DEFAULT '',
    provider    TEXT    NOT NULL,
    ciphertext  BLOB    NOT NULL,
    nonce       BLOB    NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (workspace, provider)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS settings;
-- +goose StatementEnd
