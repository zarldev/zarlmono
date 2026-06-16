-- name: GetSetting :one
-- Look up a single (workspace, key) pair. Returns sql.ErrNoRows when
-- absent; the store layer does the workspace -> global fallback by
-- calling this twice (workspace first, '' second).
SELECT value FROM settings WHERE workspace = ? AND key = ?;

-- name: ListSettingsByWorkspace :many
-- All rows for a workspace (or '' for global). Caller merges the two
-- result sets to compute the effective settings map.
SELECT key, value FROM settings WHERE workspace = ? ORDER BY key;

-- name: UpsertSetting :exec
INSERT INTO settings (workspace, key, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (workspace, key) DO UPDATE SET
    value      = excluded.value,
    updated_at = excluded.updated_at;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE workspace = ? AND key = ?;
