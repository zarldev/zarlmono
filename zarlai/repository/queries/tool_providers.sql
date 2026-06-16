-- name: ListToolProviders :many
SELECT id, name, type, enabled, config, created_at, updated_at
FROM tool_providers ORDER BY name;

-- name: GetToolProvider :one
SELECT id, name, type, enabled, config, created_at, updated_at
FROM tool_providers WHERE name = ?;

-- name: GetToolProviderByID :one
SELECT id, name, type, enabled, config, created_at, updated_at
FROM tool_providers WHERE id = ?;

-- name: CreateToolProvider :exec
INSERT INTO tool_providers (id, name, type, enabled, config)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateToolProvider :exec
UPDATE tool_providers SET enabled = ?, config = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteToolProvider :exec
DELETE FROM tool_providers WHERE id = ?;
