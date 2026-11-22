-- +goose Up
-- Child attempts belong to a recorded turn's first root request receipt, not a
-- manufactured child request. No accounting is inferred for historical sessions.
CREATE TABLE session_child_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    owner_request_batch_id TEXT NOT NULL,
    child_task_id TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (session_id, child_task_id, attempt),
    FOREIGN KEY (session_id, owner_request_batch_id)
        REFERENCES session_history_batches(session_id, batch_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE session_child_provider_accounting;
