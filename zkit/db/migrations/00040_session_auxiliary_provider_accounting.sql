-- +goose Up
-- Each outer auxiliary provider stream belongs to an existing same-session
-- history receipt. No accounting is inferred for historical sessions.
CREATE TABLE session_auxiliary_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    invocation_id TEXT NOT NULL,
    owner_request_batch_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (session_id, invocation_id),
    FOREIGN KEY (session_id, owner_request_batch_id)
        REFERENCES session_history_batches(session_id, batch_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE session_auxiliary_provider_accounting;
