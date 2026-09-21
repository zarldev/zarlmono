-- +goose Up
CREATE TABLE session_checkpoints (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    checkpoint_id TEXT NOT NULL,
    source_session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    source_revision INTEGER NOT NULL CHECK (source_revision >= 0),
    boundary_id TEXT NOT NULL,
    format_version INTEGER NOT NULL CHECK (format_version > 0),
    payload BLOB NOT NULL CHECK (length(payload) > 0 AND length(payload) <= 1048576),
    checksum TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    PRIMARY KEY (session_id, checkpoint_id)
);
CREATE INDEX session_checkpoints_retention ON session_checkpoints(session_id, pinned, created_at_ms, checkpoint_id);

-- Source identifiers are provenance, not foreign keys: deleting a source must
-- never cascade to its independently saved continuation.
CREATE TABLE session_branches (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    source_session_id TEXT NOT NULL,
    source_checkpoint_id TEXT NOT NULL,
    source_revision INTEGER NOT NULL CHECK (source_revision >= 0),
    checkpoint_checksum TEXT NOT NULL,
    checksum TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
);

-- Expire before activity is refreshed, so reopening an old session cannot
-- resurrect expired rewind capability. Pinned recovery data is exempt.
-- +goose StatementBegin
CREATE TRIGGER expire_checkpoints_before_session_activity
BEFORE UPDATE OF updated_at ON sessions
WHEN OLD.updated_at <= NEW.updated_at - 2592000
BEGIN
    DELETE FROM session_checkpoints WHERE session_id = OLD.id AND pinned = 0;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER expire_checkpoints_before_session_activity;
DROP TABLE session_branches;
DROP TABLE session_checkpoints;
