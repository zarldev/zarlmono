-- +goose Up
-- +goose StatementBegin

CREATE TABLE llm_providers (
    name          TEXT NOT NULL PRIMARY KEY,
    display_name  TEXT NOT NULL DEFAULT '',
    adapter_type  TEXT NOT NULL,
    base_url      TEXT NOT NULL DEFAULT '',
    models_url    TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    seed_models   TEXT NOT NULL DEFAULT '[]',  -- JSON array
    enabled       INTEGER NOT NULL DEFAULT 1,
    builtin       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS llm_providers;
-- +goose StatementEnd
