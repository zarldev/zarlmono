-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessionsByWorkspace :many
SELECT * FROM sessions
WHERE workspace = ?
ORDER BY updated_at DESC;

-- name: ListSessionSummariesByWorkspace :many
SELECT id, label, provider, model, created_at, updated_at, message_count
FROM sessions
WHERE workspace = ?
ORDER BY updated_at DESC;

-- name: UpsertSession :exec
INSERT INTO sessions (
    id, workspace, label, agent_name, provider, model,
    history_json, pending_json, last_usage_json, diff_bodies_json, plan_json, message_count, tool_trace_json,
    created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?
)
ON CONFLICT (id) DO UPDATE SET
    label            = excluded.label,
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
    updated_at       = excluded.updated_at;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteEmptySession :exec
-- Empty == default history/pending. Used to clean up a session that
-- the user opened but never sent a turn to.
DELETE FROM sessions
WHERE id = ? AND history_json = '[]' AND pending_json = '[]';
