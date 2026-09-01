-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessionsByWorkspace :many
SELECT * FROM sessions
WHERE workspace = ?
ORDER BY updated_at DESC;

-- name: ListSessionSummariesByWorkspace :many
SELECT id, label, label_manual, agent_name, provider, model, created_at, updated_at, message_count,
       pinned, pinned_at, changed_file_count, plan_completed_count, plan_total_count,
       CASE WHEN pending_json IS NOT NULL AND TRIM(pending_json) NOT IN ('', '[]', 'null') THEN 1 ELSE 0 END AS has_draft
FROM sessions
WHERE workspace = ?
ORDER BY pinned DESC, pinned_at DESC, updated_at DESC;

-- name: UpsertSession :exec
INSERT INTO sessions (
    id, workspace, label, label_manual, agent_name, provider, model,
    history_json, pending_json, last_usage_json, diff_bodies_json, plan_json, message_count, tool_trace_json,
    created_at, updated_at, changed_file_count, plan_completed_count, plan_total_count
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?
)
ON CONFLICT (id) DO UPDATE SET
    label            = excluded.label,
    label_manual     = excluded.label_manual,
    agent_name       = excluded.agent_name,
    provider         = excluded.provider,
    model            = excluded.model,
    history_json     = excluded.history_json,
    pending_json     = excluded.pending_json,
    last_usage_json  = excluded.last_usage_json,
    diff_bodies_json = excluded.diff_bodies_json,
    plan_json        = excluded.plan_json,
    tool_trace_json  = excluded.tool_trace_json,
    message_count    = excluded.message_count,
    updated_at           = excluded.updated_at,
    changed_file_count   = excluded.changed_file_count,
    plan_completed_count = excluded.plan_completed_count,
    plan_total_count     = excluded.plan_total_count;
-- name: SaveSessionDraft :exec
INSERT INTO sessions (
    id, workspace, label, label_manual, agent_name, provider, model,
    history_json, pending_json, last_usage_json, diff_bodies_json, plan_json,
    message_count, tool_trace_json, created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    '[]', ?, '{}', '{}', '{}',
    0, '[]', ?, ?
)
ON CONFLICT(id) DO UPDATE SET
    pending_json = excluded.pending_json;

-- name: ClearSessionDraft :exec
UPDATE sessions SET pending_json = '[]' WHERE id = ?;

-- name: RenameSession :exec
UPDATE sessions
SET label = ?, label_manual = 1
WHERE id = ?;

-- name: SetSessionPinned :exec
UPDATE sessions
SET pinned = ?, pinned_at = ?
WHERE id = ?;


-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteEmptySession :exec
-- Empty == default history/pending. Used to clean up a session that
-- the user opened but never sent a turn to.
DELETE FROM sessions
WHERE id = ? AND history_json = '[]' AND pending_json = '[]';
