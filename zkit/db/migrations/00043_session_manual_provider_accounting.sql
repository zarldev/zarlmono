-- +goose Up
-- Manual compaction evidence belongs to a durable session, not to a turn receipt.
CREATE TABLE session_manual_provider_accounting (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    invocation_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    payload_json BLOB NOT NULL,
    checksum TEXT NOT NULL,
    UNIQUE (session_id, invocation_id)
);

-- +goose Down
DROP TABLE session_manual_provider_accounting;
