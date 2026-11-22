-- name: InsertHeadlessChildProviderAccounting :exec
INSERT INTO headless_child_provider_accounting (run_id, child_task_id, attempt, version, payload_json, checksum)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetHeadlessChildProviderAccounting :one
SELECT sequence,
    CAST(CASE WHEN typeof(run_id) = 'text' THEN run_id ELSE '' END AS TEXT) AS run_id,
    CAST(CASE WHEN typeof(child_task_id) = 'text' THEN child_task_id ELSE '' END AS TEXT) AS child_task_id,
    CAST(CASE WHEN typeof(attempt) = 'integer' THEN attempt ELSE 0 END AS INTEGER) AS attempt,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    CAST(CASE WHEN typeof(checksum) = 'text' THEN checksum ELSE '' END AS TEXT) AS checksum
FROM headless_child_provider_accounting
WHERE run_id = ? AND child_task_id = ? AND attempt = ?;

-- name: ListHeadlessChildProviderAccounting :many
SELECT sequence,
    CAST(CASE WHEN typeof(run_id) = 'text' THEN run_id ELSE '' END AS TEXT) AS run_id,
    CAST(CASE WHEN typeof(child_task_id) = 'text' THEN child_task_id ELSE '' END AS TEXT) AS child_task_id,
    CAST(CASE WHEN typeof(attempt) = 'integer' THEN attempt ELSE 0 END AS INTEGER) AS attempt,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    CAST(CASE WHEN typeof(checksum) = 'text' THEN checksum ELSE '' END AS TEXT) AS checksum
FROM headless_child_provider_accounting
WHERE run_id = ? AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);
