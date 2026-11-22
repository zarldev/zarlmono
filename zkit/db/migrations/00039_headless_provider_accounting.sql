-- +goose Up
-- Run-owned immutable provider occurrences. Historical summaries imply no evidence.
CREATE TABLE headless_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    pursue_attempt INTEGER NOT NULL,
    provider_ordinal INTEGER NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (run_id, pursue_attempt, provider_ordinal),
    FOREIGN KEY (run_id) REFERENCES headless_runs(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE headless_provider_accounting;
