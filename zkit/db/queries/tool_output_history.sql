-- name: AppendToolOutputHistory :exec
INSERT INTO tool_output_history (session_id, tool_call_id, tool_name, execution_id, parent_tool_call_id, args_json, parameters_json, output, parts_json, effects_json, success, error, kind, created_at, parent_execution_id, task_id, attempt, sequence, dispatched)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListToolOutputHistory :many
SELECT * FROM tool_output_history WHERE session_id = ? ORDER BY id;
