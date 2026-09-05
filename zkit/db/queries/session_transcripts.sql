-- name: EnsureSessionTranscript :exec
INSERT INTO session_transcripts (session_id, revision, checksum, format_version, created_at_ms, updated_at_ms)
VALUES (sqlc.arg(session_id), 0, CAST(sqlc.arg(checksum) AS TEXT), sqlc.arg(format_version), sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms))
ON CONFLICT(session_id) DO NOTHING;

-- name: AdvanceSessionTranscript :execresult
UPDATE session_transcripts
SET revision = sqlc.arg(revision), checksum = CAST(sqlc.arg(checksum) AS TEXT),
    format_version = sqlc.arg(format_version), updated_at_ms = sqlc.arg(updated_at_ms)
WHERE session_id = sqlc.arg(session_id) AND revision = sqlc.arg(expected_revision);

-- name: UpsertSessionTranscriptEntry :exec
INSERT INTO session_transcript_entries (
    session_id, sequence, entry_id, parent_id, turn_id, kind, payload_json, revision
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, entry_id) DO UPDATE SET
    parent_id    = excluded.parent_id,
    turn_id      = excluded.turn_id,
    kind         = excluded.kind,
    payload_json = excluded.payload_json,
    revision     = excluded.revision
WHERE excluded.revision > session_transcript_entries.revision;

-- name: GetSessionTranscript :one
SELECT session_id, revision, CAST(checksum AS TEXT) AS checksum, format_version, created_at_ms, updated_at_ms
FROM session_transcripts
WHERE session_id = ?;

-- name: ListSessionTranscriptEntries :many
SELECT session_id, sequence, entry_id, parent_id, turn_id, kind, payload_json, revision
FROM session_transcript_entries
WHERE session_id = ?
ORDER BY sequence;
