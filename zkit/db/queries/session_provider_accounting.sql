-- name: InsertSessionProviderAccounting :exec
INSERT INTO session_provider_accounting (session_id, request_batch_id, version, payload_json, checksum)
VALUES (?, ?, ?, ?, ?);

-- name: GetSessionProviderAccounting :one
SELECT sequence, session_id, request_batch_id,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM session_provider_accounting
WHERE session_id = ? AND request_batch_id = ?;

-- name: ListSessionProviderAccounting :many
SELECT sequence, session_id, request_batch_id,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM session_provider_accounting
WHERE session_id = ? AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);
