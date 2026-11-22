-- +goose Up
-- Settled accounting belongs to an existing recorded request receipt and shares
-- its session deletion lifetime. No historical accounting is inferred.
CREATE TABLE session_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    request_batch_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (session_id, request_batch_id),
    FOREIGN KEY (session_id, request_batch_id)
        REFERENCES session_history_batches(session_id, batch_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE session_provider_accounting;
