-- +goose Up
-- Content-addressed values and prefix nodes are immutable and deliberately have
-- no session owner. Deleting a source or expiring a checkpoint cannot delete a
-- prefix still used by a child. Reachability GC is not checkpoint retention.
CREATE TABLE session_history_values (
    id TEXT PRIMARY KEY,
    payload BLOB NOT NULL
);
CREATE TABLE session_history_nodes (
    id TEXT PRIMARY KEY,
    parent_id TEXT NOT NULL,
    value_id TEXT NOT NULL REFERENCES session_history_values(id)
);
CREATE TABLE session_history_heads (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    head TEXT NOT NULL,
    PRIMARY KEY (session_id, kind)
);
CREATE TABLE session_checkpoint_history (
    session_id TEXT NOT NULL,
    checkpoint_id TEXT NOT NULL,
    transcript_head TEXT NOT NULL,
    replay_head TEXT NOT NULL,
    state_id TEXT NOT NULL REFERENCES session_history_values(id),
    PRIMARY KEY (session_id, checkpoint_id),
    FOREIGN KEY (session_id, checkpoint_id) REFERENCES session_checkpoints(session_id, checkpoint_id) ON DELETE CASCADE
);
CREATE TABLE session_history_batches (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    batch_id TEXT NOT NULL,
    checksum TEXT NOT NULL,
    PRIMARY KEY (session_id, batch_id)
);
CREATE TABLE session_model_context (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    history_head TEXT NOT NULL,
    generation INTEGER NOT NULL,
    request_json BLOB NOT NULL
);
-- +goose StatementBegin
CREATE TRIGGER session_history_values_immutable BEFORE UPDATE ON session_history_values
BEGIN SELECT RAISE(ABORT, 'immutable history value'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_history_nodes_immutable BEFORE UPDATE ON session_history_nodes
BEGIN SELECT RAISE(ABORT, 'immutable history node'); END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER session_history_nodes_immutable;
DROP TRIGGER session_history_values_immutable;
DROP TABLE session_history_batches;
DROP TABLE session_model_context;
DROP TABLE session_checkpoint_history;
DROP TABLE session_history_heads;
DROP TABLE session_history_nodes;
DROP TABLE session_history_values;
