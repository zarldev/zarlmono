-- +goose Up
-- Outer auxiliary streams have the headless run's lifetime, independent of
-- pursue attempts, root requests and session history. No historical backfill.
CREATE TABLE headless_auxiliary_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    invocation_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (run_id, invocation_id),
    FOREIGN KEY (run_id) REFERENCES headless_runs(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE headless_auxiliary_provider_accounting;
