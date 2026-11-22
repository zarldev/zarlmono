-- name: InsertSessionChildProviderAccounting :exec
INSERT INTO session_child_provider_accounting (session_id, owner_request_batch_id, child_task_id, attempt, version, payload_json, checksum)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionChildProviderAccounting :one
SELECT sequence, session_id, owner_request_batch_id, child_task_id,
    CAST(CASE WHEN typeof(attempt) = 'integer' THEN attempt ELSE 0 END AS INTEGER) AS attempt,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM session_child_provider_accounting
WHERE session_id = ? AND child_task_id = ? AND attempt = ?;

-- name: ListSessionChildProviderAccounting :many
SELECT sequence, session_id, owner_request_batch_id, child_task_id,
    CAST(CASE WHEN typeof(attempt) = 'integer' THEN attempt ELSE 0 END AS INTEGER) AS attempt,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM session_child_provider_accounting
WHERE session_id = ? AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);
