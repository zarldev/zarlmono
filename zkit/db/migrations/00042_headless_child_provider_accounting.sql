-- +goose Up
-- Descendant provider streams belong to the headless run, not the root attempt.
CREATE TABLE headless_child_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    child_task_id TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (run_id, child_task_id, attempt),
    FOREIGN KEY (run_id) REFERENCES headless_runs(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE headless_child_provider_accounting;
