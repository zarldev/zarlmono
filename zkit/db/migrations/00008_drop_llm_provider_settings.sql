-- +goose Up
-- +goose StatementBegin
-- The per-provider typed-settings path was removed: the typed apply
-- framework was a never-wired footgun (storing a setting then building
-- the provider failed with ErrUnknownSetting), and nothing reads or
-- writes this table anymore. Drop it.
DROP TABLE IF EXISTS llm_provider_settings;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE llm_provider_settings (
    workspace  TEXT NOT NULL DEFAULT '',
    provider   TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (workspace, provider, key),
    FOREIGN KEY (provider) REFERENCES llm_providers(name) ON DELETE CASCADE
);
-- +goose StatementEnd
