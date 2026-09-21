-- name: AcquireCheckpointWrite :exec
-- Promote the caller's deferred transaction to SQLite write ownership before
-- reading its CAS snapshot. A zero-row UPDATE acquires the writer lock without
-- changing rows or firing row triggers. Competing writers finish before our
-- observation rather than causing SQLITE_BUSY_SNAPSHOT after it.
UPDATE sessions SET id = id WHERE 0;

-- name: InsertSessionCheckpoint :exec
INSERT INTO session_checkpoints (session_id, checkpoint_id, source_session_id, workspace, source_revision, boundary_id, format_version, payload, checksum, created_at_ms, pinned)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionCheckpoint :one
SELECT session_id, checkpoint_id, source_session_id, workspace, source_revision, boundary_id, format_version, payload, checksum, created_at_ms, pinned
FROM session_checkpoints WHERE session_id = ? AND checkpoint_id = ?;

-- name: ListSessionCheckpoints :many
SELECT checkpoint_id, source_session_id, workspace, source_revision, boundary_id, format_version, checksum, created_at_ms, pinned, length(payload) AS payload_bytes
FROM session_checkpoints WHERE session_id = ?
ORDER BY created_at_ms, checkpoint_id;

-- name: DeleteSessionCheckpoint :exec
DELETE FROM session_checkpoints WHERE session_id = ? AND checkpoint_id = ? AND pinned = 0;

-- name: PinSessionCheckpoint :execrows
UPDATE session_checkpoints SET pinned = ? WHERE session_id = ? AND checkpoint_id = ?;

-- name: ExpireSessionCheckpoints :execrows
DELETE FROM session_checkpoints
WHERE pinned = 0 AND session_id IN (SELECT id FROM sessions WHERE updated_at <= sqlc.arg(inactive_before));

-- name: SessionCheckpointActivity :one
SELECT workspace, updated_at FROM sessions WHERE id = ?;

-- name: InsertSessionBranch :exec
INSERT INTO session_branches (session_id, source_session_id, source_checkpoint_id, source_revision, checkpoint_checksum, checksum, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionBranch :one
SELECT session_id, source_session_id, source_checkpoint_id, source_revision, checkpoint_checksum, checksum, created_at_ms
FROM session_branches WHERE session_id = ?;

-- name: TouchCheckpointSession :exec
UPDATE sessions SET updated_at = ? WHERE id = ?;
