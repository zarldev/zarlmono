-- name: GetActivePrompt :one
SELECT id, name, content, active, created_at, updated_at
FROM prompts WHERE active = TRUE LIMIT 1;

-- name: ListPrompts :many
SELECT id, name, content, active, created_at, updated_at
FROM prompts ORDER BY updated_at DESC;

-- name: CreatePrompt :exec
INSERT INTO prompts (id, name, content, active) VALUES (?, ?, ?, ?);

-- name: UpdatePromptContent :exec
UPDATE prompts SET content = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetPromptActive :exec
UPDATE prompts SET active = (id = ?), updated_at = CURRENT_TIMESTAMP;

-- name: DeletePrompt :exec
DELETE FROM prompts WHERE id = ?;
