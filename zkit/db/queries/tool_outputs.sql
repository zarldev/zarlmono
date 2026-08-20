-- name: InsertToolOutput :exec
INSERT INTO tool_outputs (session_id, tool_call_id, tool_name, args_json, output, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, tool_call_id) DO UPDATE SET
    tool_name = excluded.tool_name,
    args_json = excluded.args_json,
    output    = excluded.output,
    created_at = excluded.created_at;

-- name: ListToolOutputsBySession :many
SELECT * FROM tool_outputs
WHERE session_id = ?
ORDER BY created_at, id;

-- name: GetToolOutput :one
SELECT * FROM tool_outputs
WHERE session_id = ? AND tool_call_id = ?;

-- name: ListToolOutputSummariesBySession :many
SELECT id, session_id, tool_call_id, tool_name, args_json, created_at
FROM tool_outputs
WHERE session_id = ?
ORDER BY created_at, id;
