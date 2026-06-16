-- name: GetToolDescriptionOverride :one
SELECT name, description, updated_at
FROM tool_description_overrides WHERE name = ?;

-- name: ListToolDescriptionOverrides :many
SELECT name, description, updated_at
FROM tool_description_overrides ORDER BY name;

-- name: UpsertToolDescriptionOverride :exec
INSERT INTO tool_description_overrides (name, description)
VALUES (?, ?)
ON DUPLICATE KEY UPDATE
  description = VALUES(description),
  updated_at = CURRENT_TIMESTAMP;

-- name: DeleteToolDescriptionOverride :exec
DELETE FROM tool_description_overrides WHERE name = ?;
