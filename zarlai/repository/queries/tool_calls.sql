-- name: LogToolCall :exec
INSERT INTO tool_calls (id, session_id, tool_name, provider, args, result, error, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListToolCalls :many
SELECT id, session_id, tool_name, provider, args, result, error, duration_ms, created_at
FROM tool_calls ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CountToolCalls :one
SELECT COUNT(*) as count FROM tool_calls;

-- name: ToolCallStats :many
SELECT tool_name, provider,
    COUNT(*) as total_calls,
    AVG(duration_ms) as avg_duration_ms,
    SUM(CASE WHEN error != '' THEN 1 ELSE 0 END) as error_count
FROM tool_calls
GROUP BY tool_name, provider
ORDER BY total_calls DESC;

-- name: DeleteAllToolCalls :execrows
DELETE FROM tool_calls;
