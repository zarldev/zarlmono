-- name: GetTaskProfileOverride :one
SELECT profile_name, model, prompt_prefix, max_iterations, tool_names, updated_at
FROM task_profile_overrides
WHERE profile_name = ?;

-- name: UpsertTaskProfileOverride :exec
INSERT INTO task_profile_overrides (profile_name, model, prompt_prefix, max_iterations, tool_names)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  model = VALUES(model),
  prompt_prefix = VALUES(prompt_prefix),
  max_iterations = VALUES(max_iterations),
  tool_names = VALUES(tool_names);

-- name: DeleteTaskProfileOverride :exec
DELETE FROM task_profile_overrides WHERE profile_name = ?;

-- name: ListTaskProfileOverrides :many
SELECT profile_name, model, prompt_prefix, max_iterations, tool_names, updated_at
FROM task_profile_overrides
ORDER BY profile_name;
