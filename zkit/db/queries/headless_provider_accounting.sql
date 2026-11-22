-- name: EnsureHeadlessRecordingStart :exec
INSERT INTO headless_runs (id, workspace, base_commit, prompt, started_at, provider, model)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING;

-- name: InsertHeadlessProviderAccounting :exec
INSERT INTO headless_provider_accounting (run_id, pursue_attempt, provider_ordinal, version, payload_json, checksum)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetHeadlessProviderAccounting :one
SELECT sequence, run_id,
    CAST(CASE WHEN typeof(pursue_attempt) = 'integer' THEN pursue_attempt ELSE 0 END AS INTEGER) AS pursue_attempt,
    CAST(CASE WHEN typeof(provider_ordinal) = 'integer' THEN provider_ordinal ELSE 0 END AS INTEGER) AS provider_ordinal,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM headless_provider_accounting
WHERE run_id = ? AND pursue_attempt = ? AND provider_ordinal = ?;

-- name: ListHeadlessProviderAccounting :many
SELECT sequence, run_id,
    CAST(CASE WHEN typeof(pursue_attempt) = 'integer' THEN pursue_attempt ELSE 0 END AS INTEGER) AS pursue_attempt,
    CAST(CASE WHEN typeof(provider_ordinal) = 'integer' THEN provider_ordinal ELSE 0 END AS INTEGER) AS provider_ordinal,
    CAST(CASE WHEN typeof(version) = 'integer' THEN version ELSE 0 END AS INTEGER) AS version,
    CAST(CASE WHEN typeof(payload_json) = 'blob' AND length(payload_json) <= 1048576
        THEN payload_json ELSE X'' END AS BLOB) AS payload_json,
    checksum
FROM headless_provider_accounting
WHERE run_id = ? AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);
