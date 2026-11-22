-- name: InsertHeadlessAuxiliaryProviderAccounting :exec
INSERT INTO headless_auxiliary_provider_accounting (run_id, invocation_id, version, payload_json, checksum)
VALUES (?, ?, ?, ?, ?);

-- name: GetHeadlessAuxiliaryProviderAccounting :one
SELECT sequence,
    CAST(CASE WHEN typeof(run_id) = 'text' THEN run_id ELSE '' END AS TEXT) AS run_id,
    CAST(CASE WHEN typeof(invocation_id) = 'text' THEN invocation_id ELSE '' END AS TEXT) AS invocation_id,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    CAST(CASE WHEN typeof(checksum) = 'text' THEN checksum ELSE '' END AS TEXT) AS checksum
FROM headless_auxiliary_provider_accounting
WHERE run_id = ? AND invocation_id = ?;

-- name: ListHeadlessAuxiliaryProviderAccounting :many
SELECT sequence,
    CAST(CASE WHEN typeof(run_id) = 'text' THEN run_id ELSE '' END AS TEXT) AS run_id,
    CAST(CASE WHEN typeof(invocation_id) = 'text' THEN invocation_id ELSE '' END AS TEXT) AS invocation_id,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    CAST(CASE WHEN typeof(checksum) = 'text' THEN checksum ELSE '' END AS TEXT) AS checksum
FROM headless_auxiliary_provider_accounting
WHERE run_id = ? AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);
